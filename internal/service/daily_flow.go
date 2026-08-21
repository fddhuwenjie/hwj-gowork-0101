package service

import (
	"context"
	"database/sql"

	"metalmics/internal/domain"
	"metalmics/internal/repository"
)

// SubmitSpectrumReport 提交光谱分析报告（事务用例三：
// 报告入库 + 样本状态更新 + 审计，任一失败整体回滚）。
//
// 结论依据批次牌号的 active 规则版本计算并固化（R02）；
// 初检样本在 created 状态可提交；已 tested 的留样仅当存在已批准的
// 复验任务时才允许再次提交（复验报告）。以 report_no 作为幂等键。
func (s *DailyService) SubmitSpectrumReport(ctx context.Context, rep *domain.SpectrumReport, actor string) (created bool, err error) {
	if err := rep.Validate(); err != nil {
		return false, err
	}
	err = s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		specRepo := repository.NewSpectrumRepo(tx)
		// 幂等重放：同报告编号直接返回既有记录
		existing, err := specRepo.GetByReportNo(ctx, rep.ReportNo)
		if err != nil {
			return err
		}
		if existing != nil {
			*rep = *existing
			return nil
		}
		sampleRepo := repository.NewSampleRepo(tx)
		sample, err := sampleRepo.GetByID(ctx, rep.SampleID)
		if err != nil {
			return err
		}
		if sample == nil {
			return domain.NotFound("sample", rep.SampleID)
		}
		plan, err := repository.NewSamplingPlanRepo(tx).GetByID(ctx, sample.PlanID)
		if err != nil {
			return err
		}
		lot, err := loadLot(ctx, tx, plan.LotID, 0)
		if err != nil {
			return err
		}
		switch sample.Status {
		case domain.SampleStatusCreated:
			if lot.Status != domain.LotStatusSampled && lot.Status != domain.LotStatusAnalyzed {
				return domain.InvalidTransition("material_lot", string(lot.Status), "submit_spectrum")
			}
		case domain.SampleStatusTested:
			// 复验报告：必须存在引用该留样的已批准复验任务
			task, err := repository.NewRetestRepo(tx).GetOpenByLot(ctx, lot.ID)
			if err != nil {
				return err
			}
			if task == nil || task.Status != domain.RetestStatusApproved || task.SampleID != sample.ID {
				return domain.RuleViolation(domain.RuleOriginalSampleRetained,
					"样本已完成初检，且没有引用该留样的已批准复验任务")
			}
		default:
			return domain.InvalidTransition("sample", string(sample.Status), "submit_spectrum")
		}
		rule, err := repository.NewGradeRuleRepo(tx).GetActiveByGrade(ctx, lot.Grade)
		if err != nil {
			return err
		}
		if err := domain.RequireActiveGradeRule(rule, lot.Grade); err != nil {
			return err
		}
		rep.RuleID = rule.ID
		rep.Violations = domain.CheckReadingsInRange(rule.Elements, rep.Readings)
		if len(rep.Violations) == 0 {
			rep.Conclusion = domain.SpectrumInRange
		} else {
			rep.Conclusion = domain.SpectrumOutOfRange
		}
		if err := specRepo.Insert(ctx, rep); err != nil {
			return err
		}
		if sample.Status == domain.SampleStatusCreated {
			ok, err := sampleRepo.UpdateStatus(ctx, sample.ID, sample.Version, domain.SampleStatusTested)
			if err != nil {
				return err
			}
			if !ok {
				return domain.VersionConflict("sample", sample.ID, sample.Version, -1)
			}
		}
		created = true
		return audit(ctx, tx, "spectrum_report", rep.ID, "submit", actor,
			map[string]interface{}{"report_no": rep.ReportNo, "sample_id": rep.SampleID, "conclusion": rep.Conclusion})
	})
	return created, err
}

// ListSpectrumBySample 查询样本的光谱报告列表。
func (s *DailyService) ListSpectrumBySample(ctx context.Context, sampleID int64) ([]domain.SpectrumReport, error) {
	return repository.NewSpectrumRepo(s.store.DB()).ListBySample(ctx, sampleID)
}

// AnalyzeLot 完成光谱分析（事务：批次 sampled->analyzed + 审计）。
// 要求全部初检样本均已出具报告。
func (s *DailyService) AnalyzeLot(ctx context.Context, lotID, expectedVersion int64, actor string) (*domain.MaterialLot, error) {
	var out *domain.MaterialLot
	err := s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		lot, err := loadLot(ctx, tx, lotID, expectedVersion)
		if err != nil {
			return err
		}
		if lot.Status != domain.LotStatusSampled {
			return domain.InvalidTransition("material_lot", string(lot.Status), string(domain.LotStatusAnalyzed))
		}
		reports, err := repository.NewSpectrumRepo(tx).ListBySampleKind(ctx, lotID, domain.SampleKindInitial)
		if err != nil {
			return err
		}
		plan, err := repository.NewSamplingPlanRepo(tx).GetByLot(ctx, lotID)
		if err != nil {
			return err
		}
		if plan == nil || len(reports) != plan.RequiredCount {
			return domain.RuleViolation(domain.RuleSampleCountComplete,
				"尚有初检样本未出具光谱报告，不允许完成分析")
		}
		if err := transitionLot(ctx, tx, lot, domain.LotStatusAnalyzed, actor, "analyze",
			map[string]interface{}{"reports": len(reports)}, nil, nil, nil, nil); err != nil {
			return err
		}
		out = lot
		return nil
	})
	return out, err
}

// RegisterCertificate 登记材质证明（事务：证明入库 + 审计）。以 cert_no 作为幂等键。
func (s *DailyService) RegisterCertificate(ctx context.Context, cert *domain.MillCertificate, actor string) (created bool, err error) {
	if err := cert.Validate(); err != nil {
		return false, err
	}
	err = s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		certRepo := repository.NewCertificateRepo(tx)
		if err := certRepo.Insert(ctx, cert); err != nil {
			if !domain.IsCode(err, domain.ErrCodeDuplicate) {
				return err
			}
			existing, gerr := certRepo.GetByCertNo(ctx, cert.CertNo)
			if gerr != nil {
				return gerr
			}
			*cert = *existing
			return nil
		}
		lot, err := loadLot(ctx, tx, cert.LotID, 0)
		if err != nil {
			return err
		}
		if lot.Status.IsTerminal() {
			return domain.InvalidTransition("material_lot", string(lot.Status), "register_certificate")
		}
		created = true
		return audit(ctx, tx, "mill_certificate", cert.ID, "register", actor,
			map[string]interface{}{"cert_no": cert.CertNo, "lot_id": cert.LotID})
	})
	return created, err
}

// ListCertificates 查询批次的材质证明列表。
func (s *DailyService) ListCertificates(ctx context.Context, lotID int64) ([]domain.MillCertificate, error) {
	return repository.NewCertificateRepo(s.store.DB()).ListByLot(ctx, lotID)
}

// VerifyCertificate 材质证明核对（事务：R03 牌号/炉批号一致性校验 + 标记核对 + 审计）。
func (s *DailyService) VerifyCertificate(ctx context.Context, certID, expectedVersion int64, actor string) (*domain.MillCertificate, error) {
	var out *domain.MillCertificate
	err := s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		certRepo := repository.NewCertificateRepo(tx)
		cert, err := certRepo.GetByID(ctx, certID)
		if err != nil {
			return err
		}
		if cert == nil {
			return domain.NotFound("mill_certificate", certID)
		}
		if expectedVersion > 0 && cert.Version != expectedVersion {
			return domain.VersionConflict("mill_certificate", certID, expectedVersion, cert.Version)
		}
		if cert.Verified {
			out = cert // 幂等重放：已核对直接返回
			return nil
		}
		lot, err := loadLot(ctx, tx, cert.LotID, 0)
		if err != nil {
			return err
		}
		if err := domain.RequireCertMatchesLot(cert, lot); err != nil {
			return err
		}
		ok, err := certRepo.MarkVerified(ctx, cert.ID, cert.Version, actor, nowTime())
		if err != nil {
			return err
		}
		if !ok {
			return domain.VersionConflict("mill_certificate", cert.ID, cert.Version, -1)
		}
		if err := audit(ctx, tx, "mill_certificate", cert.ID, "verify", actor,
			map[string]interface{}{"cert_no": cert.CertNo}); err != nil {
			return err
		}
		cert.Verified = true
		cert.Version++
		out = cert
		return nil
	})
	return out, err
}

// JudgeLot 符合性判定（事务用例四：
// R04 材质证明强制 + 光谱符合性计算 + 结论入库 + 批次 analyzed->judged + 审计）。
//
// 判定结果 = 材质证明核对通过 且 全部初检光谱报告在牌号范围内。
// 结果为 fail 时批次同样进入 judged，由接收/复验/处置流程分流。
func (s *DailyService) JudgeLot(ctx context.Context, lotID, expectedVersion int64, decidedBy, reason string) (*domain.ConformityConclusion, error) {
	if decidedBy == "" {
		return nil, domain.Validation("decided_by", "判定人不能为空")
	}
	var out *domain.ConformityConclusion
	err := s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		lot, err := loadLot(ctx, tx, lotID, expectedVersion)
		if err != nil {
			return err
		}
		if lot.Status != domain.LotStatusAnalyzed {
			return domain.InvalidTransition("material_lot", string(lot.Status), string(domain.LotStatusJudged))
		}
		certificates, err := repository.NewCertificateRepo(tx).ListByLot(ctx, lotID)
		if err != nil {
			return err
		}
		var cert *domain.MillCertificate
		for i := range certificates {
			cert = &certificates[i]
		}
		if err := domain.RequireCertForJudgment(cert); err != nil {
			return err
		}
		reports, err := repository.NewSpectrumRepo(tx).ListBySampleKind(ctx, lotID, domain.SampleKindInitial)
		if err != nil {
			return err
		}
		if len(reports) == 0 {
			return domain.RuleViolation(domain.RuleSpectrumWithinGradeRange, "无初检光谱报告，不得判定")
		}
		spectrumOK := domain.SpectrumAllInRange(reports)
		result := domain.ResultFail
		if spectrumOK {
			result = domain.ResultPass
		}
		conclusion := &domain.ConformityConclusion{
			LotID: lotID, Round: domain.RoundInitial, Result: result,
			CertOK: true, SpectrumOK: spectrumOK, Reason: reason, DecidedBy: decidedBy,
		}
		if err := conclusion.Validate(); err != nil {
			return err
		}
		if err := repository.NewConclusionRepo(tx).Insert(ctx, conclusion); err != nil {
			return err
		}
		resultStr := string(result)
		if err := transitionLot(ctx, tx, lot, domain.LotStatusJudged, decidedBy, "judge",
			map[string]interface{}{"result": result, "cert_ok": true, "spectrum_ok": spectrumOK},
			&resultStr, nil, nil, nil); err != nil {
			return err
		}
		out = conclusion
		return nil
	})
	return out, err
}

// ListConclusions 查询批次的符合性结论列表。
func (s *DailyService) ListConclusions(ctx context.Context, lotID int64) ([]domain.ConformityConclusion, error) {
	return repository.NewConclusionRepo(s.store.DB()).ListByLot(ctx, lotID)
}

// AcceptLot 接收确认（事务：R11/R12 校验 + 批次 judged->accepted + 审计）。
func (s *DailyService) AcceptLot(ctx context.Context, lotID, expectedVersion int64, actor string) (*domain.MaterialLot, error) {
	var out *domain.MaterialLot
	err := s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		lot, err := loadLot(ctx, tx, lotID, expectedVersion)
		if err != nil {
			return err
		}
		if err := domain.RequireNotQuarantinedForAccept(lot); err != nil {
			return err
		}
		hasConcession, err := repository.NewDispositionRepo(tx).HasApprovedConcession(ctx, lotID)
		if err != nil {
			return err
		}
		if err := domain.RequirePassOrConcessionForAccept(lot, hasConcession); err != nil {
			return err
		}
		now := nowTime()
		if err := transitionLot(ctx, tx, lot, domain.LotStatusAccepted, actor, "accept",
			map[string]interface{}{"final_result": lot.FinalResult(), "concession": hasConcession},
			nil, nil, &actor, &now); err != nil {
			return err
		}
		out = lot
		return nil
	})
	return out, err
}
