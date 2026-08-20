package service

import (
	"context"
	"database/sql"

	"metalmics/internal/domain"
	"metalmics/internal/repository"
)

// ReviewService 承载“复核归档”流程：异议复验的申请、批准与结论。
// 复验结论覆盖初检结论时必须共同决定（R09），
// 复验必须基于留存的初检原样（R08）。
type ReviewService struct {
	store *Store
}

// NewReviewService 构造复核归档服务。
func NewReviewService(store *Store) *ReviewService {
	return &ReviewService{store: store}
}

// RequestRetest 发起异议复验（事务：R07/R08 校验 + 任务入库 + 批次 ->retesting + 审计）。
// 同批次同时只允许一个未关闭任务，重复提交返回既有任务（幂等）。
func (s *ReviewService) RequestRetest(ctx context.Context, task *domain.RetestTask, actor string) (created bool, err error) {
	if err := task.Validate(); err != nil {
		return false, err
	}
	err = s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		retestRepo := repository.NewRetestRepo(tx)
		if err := retestRepo.Insert(ctx, task); err != nil {
			if !domain.IsCode(err, domain.ErrCodeDuplicate) {
				return err
			}
			existing, gerr := retestRepo.GetOpenByLot(ctx, task.LotID)
			if gerr != nil {
				return gerr
			}
			if existing == nil {
				return err
			}
			*task = *existing
			return nil
		}
		lot, err := loadLot(ctx, tx, task.LotID, 0)
		if err != nil {
			return err
		}
		if err := domain.RequireRetestAfterJudgment(lot); err != nil {
			return err
		}
		sample, err := repository.NewSampleRepo(tx).GetByID(ctx, task.SampleID)
		if err != nil {
			return err
		}
		if err := domain.RequireOriginalSampleRetained(sample); err != nil {
			return err
		}
		plan, err := repository.NewSamplingPlanRepo(tx).GetByLot(ctx, lot.ID)
		if err != nil {
			return err
		}
		if plan == nil || plan.ID != sample.PlanID {
			return domain.RuleViolation(domain.RuleOriginalSampleRetained, "复验样本不属于该批次的取样计划")
		}
		if lot.Status == domain.LotStatusJudged {
			if err := transitionLot(ctx, tx, lot, domain.LotStatusRetesting, actor, "request_retest",
				map[string]interface{}{"task_id": task.ID, "reason": task.Reason}, nil, nil, nil, nil); err != nil {
				return err
			}
		} else if lot.Status != domain.LotStatusRejected {
			return domain.InvalidTransition("material_lot", string(lot.Status), string(domain.LotStatusRetesting))
		}
		created = true
		return audit(ctx, tx, "retest_task", task.ID, "request", actor,
			map[string]interface{}{"lot_id": task.LotID, "sample_id": task.SampleID})
	})
	return created, err
}

// ApproveRetest 批准复验任务（事务：open->approved + 审计）。
// 批准人不能与申请人为同一人。批次处于 rejected 时批准使其进入 retesting。
func (s *ReviewService) ApproveRetest(ctx context.Context, taskID, expectedVersion int64, approver string) (*domain.RetestTask, error) {
	if approver == "" {
		return nil, domain.Validation("approved_by", "批准人不能为空")
	}
	var out *domain.RetestTask
	err := s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		task, err := s.loadTask(ctx, tx, taskID, expectedVersion)
		if err != nil {
			return err
		}
		if task.Status != domain.RetestStatusOpen {
			return domain.InvalidTransition("retest_task", string(task.Status), string(domain.RetestStatusApproved))
		}
		if approver == task.RequestedBy {
			return domain.RuleViolation(domain.RuleOverrideRequiresCoDecision,
				"复验批准人不能与申请人为同一人")
		}
		lot, err := loadLot(ctx, tx, task.LotID, 0)
		if err != nil {
			return err
		}
		if lot.Status == domain.LotStatusRejected {
			// 拒收后的异议获准：批次重新进入复验流程
			if err := transitionLot(ctx, tx, lot, domain.LotStatusRetesting, approver, "approve_retest",
				map[string]interface{}{"task_id": task.ID}, nil, nil, nil, nil); err != nil {
				return err
			}
		}
		ok, err := repository.NewRetestRepo(tx).UpdateStatus(ctx, taskID, task.Version,
			domain.RetestStatusApproved, &approver)
		if err != nil {
			return err
		}
		if !ok {
			return domain.VersionConflict("retest_task", taskID, task.Version, -1)
		}
		if err := audit(ctx, tx, "retest_task", taskID, "approve", approver, nil); err != nil {
			return err
		}
		task.Status = domain.RetestStatusApproved
		task.ApprovedBy = approver
		task.Version++
		out = task
		return nil
	})
	return out, err
}

// ConcludeRetest 出具复验结论（事务用例五：
// 复验光谱证据核对 + R09 共同决定校验 + 结论入库 + 任务关闭 + 批次 retesting->judged + 审计）。
//
// 复验结论必须与留样的复验光谱报告一致：报告全部在范围内才能判 pass；
// 结论与初检结论相反（覆盖）时必须提供与判定人不同的共同决定人。
func (s *ReviewService) ConcludeRetest(ctx context.Context, taskID, expectedVersion int64,
	result domain.ConclusionResult, decidedBy, coDecidedBy, reason string) (*domain.ConformityConclusion, error) {
	if decidedBy == "" {
		return nil, domain.Validation("decided_by", "判定人不能为空")
	}
	if result != domain.ResultPass && result != domain.ResultFail {
		return nil, domain.Validation("result", "结论结果仅支持 pass/fail")
	}
	var out *domain.ConformityConclusion
	err := s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		task, err := s.loadTask(ctx, tx, taskID, expectedVersion)
		if err != nil {
			return err
		}
		if task.Status != domain.RetestStatusApproved {
			return domain.InvalidTransition("retest_task", string(task.Status), string(domain.RetestStatusConcluded))
		}
		lot, err := loadLot(ctx, tx, task.LotID, 0)
		if err != nil {
			return err
		}
		if lot.Status != domain.LotStatusRetesting {
			return domain.InvalidTransition("material_lot", string(lot.Status), string(domain.LotStatusJudged))
		}
		// 复验光谱证据：该留样的最新报告
		reports, err := repository.NewSpectrumRepo(tx).ListBySample(ctx, task.SampleID)
		if err != nil {
			return err
		}
		if len(reports) == 0 {
			return domain.RuleViolation(domain.RuleSpectrumWithinGradeRange, "复验留样尚无光谱报告，不得出具复验结论")
		}
		latest := reports[len(reports)-1]
		spectrumOK := latest.Conclusion == domain.SpectrumInRange
		expected := domain.ResultFail
		if spectrumOK {
			expected = domain.ResultPass
		}
		if result != expected {
			return domain.RuleViolation(domain.RuleSpectrumWithinGradeRange,
				"复验结论与留样光谱报告不一致")
		}
		override := string(result) != lot.InitialResult
		if err := domain.RequireCoDecisionForOverride(override, decidedBy, coDecidedBy); err != nil {
			return err
		}
		conclusion := &domain.ConformityConclusion{
			LotID: lot.ID, Round: domain.RoundRetest, Result: result,
			CertOK: true, SpectrumOK: spectrumOK, Reason: reason,
			DecidedBy: decidedBy, CoDecidedBy: coDecidedBy, OverridesPrev: override,
		}
		if err := conclusion.Validate(); err != nil {
			return err
		}
		if err := repository.NewConclusionRepo(tx).Insert(ctx, conclusion); err != nil {
			return err
		}
		retestResult := string(result)
		if err := transitionLot(ctx, tx, lot, domain.LotStatusJudged, decidedBy, "conclude_retest",
			map[string]interface{}{"task_id": task.ID, "result": result, "override": override},
			nil, &retestResult, nil, nil); err != nil {
			return err
		}
		ok, err := repository.NewRetestRepo(tx).UpdateStatus(ctx, taskID, task.Version,
			domain.RetestStatusConcluded, nil)
		if err != nil {
			return err
		}
		if !ok {
			return domain.VersionConflict("retest_task", taskID, task.Version, -1)
		}
		if err := audit(ctx, tx, "retest_task", taskID, "conclude", decidedBy,
			map[string]interface{}{"conclusion_id": conclusion.ID, "co_decided_by": coDecidedBy}); err != nil {
			return err
		}
		out = conclusion
		return nil
	})
	return out, err
}

// ListRetests 分页查询复验任务。
func (s *ReviewService) ListRetests(ctx context.Context, f domain.RetestFilter, p domain.PageRequest) (domain.Page[domain.RetestTask], error) {
	return repository.NewRetestRepo(s.store.DB()).List(ctx, f, p)
}

// loadTask 在事务内加载复验任务并校验存在性与版本。
func (s *ReviewService) loadTask(ctx context.Context, tx *sql.Tx, id, expectedVersion int64) (*domain.RetestTask, error) {
	task, err := repository.NewRetestRepo(tx).GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, domain.NotFound("retest_task", id)
	}
	if expectedVersion > 0 && task.Version != expectedVersion {
		return nil, domain.VersionConflict("retest_task", id, expectedVersion, task.Version)
	}
	return task, nil
}
