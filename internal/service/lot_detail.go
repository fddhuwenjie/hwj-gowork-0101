package service

import (
	"context"
	"database/sql"

	"metalmics/internal/domain"
	"metalmics/internal/repository"
)

// LotDetail 是批次详情聚合视图，供复核归档流程一屏审阅。
type LotDetail struct {
	Lot         *domain.MaterialLot            `json:"lot"`
	Plan        *domain.SamplingPlan           `json:"plan,omitempty"`
	Samples     []domain.Sample                `json:"samples"`
	Certificates []domain.MillCertificate      `json:"certificates"`
	Reports     []domain.SpectrumReport        `json:"reports"`
	Conclusions []domain.ConformityConclusion  `json:"conclusions"`
}

// GetLotDetail 聚合查询批次全链路数据（只读，跨 6 个实体）。
func (s *DailyService) GetLotDetail(ctx context.Context, lotID int64) (*LotDetail, error) {
	db := s.store.DB()
	lot, err := repository.NewLotRepo(db).GetByID(ctx, lotID)
	if err != nil {
		return nil, err
	}
	if lot == nil {
		return nil, domain.NotFound("material_lot", lotID)
	}
	detail := &LotDetail{
		Lot:          lot,
		Samples:      []domain.Sample{},
		Certificates: []domain.MillCertificate{},
		Reports:      []domain.SpectrumReport{},
		Conclusions:  []domain.ConformityConclusion{},
	}
	plan, err := repository.NewSamplingPlanRepo(db).GetByLot(ctx, lotID)
	if err != nil {
		return nil, err
	}
	detail.Plan = plan
	if plan != nil {
		samples, err := repository.NewSampleRepo(db).ListByPlan(ctx, plan.ID)
		if err != nil {
			return nil, err
		}
		detail.Samples = samples
	}
	certs, err := repository.NewCertificateRepo(db).ListByLot(ctx, lotID)
	if err != nil {
		return nil, err
	}
	detail.Certificates = certs
	reports, err := repository.NewSpectrumRepo(db).ListByLot(ctx, lotID)
	if err != nil {
		return nil, err
	}
	detail.Reports = reports
	conclusions, err := repository.NewConclusionRepo(db).ListByLot(ctx, lotID)
	if err != nil {
		return nil, err
	}
	detail.Conclusions = conclusions
	return detail, nil
}

// RejectLot 拒收确认（事务：批次 judged->rejected + 审计）。
// 仅允许对最终结论为 fail 的批次执行。
func (s *DailyService) RejectLot(ctx context.Context, lotID, expectedVersion int64, actor, reason string) (*domain.MaterialLot, error) {
	if reason == "" {
		return nil, domain.Validation("reason", "拒收原因不能为空")
	}
	var out *domain.MaterialLot
	err := s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		lot, err := loadLot(ctx, tx, lotID, expectedVersion)
		if err != nil {
			return err
		}
		if lot.FinalResult() != string(domain.ResultFail) {
			return domain.RuleViolation(domain.RuleAcceptRequiresPassOrConcession,
				"仅结论为不符合的批次才允许拒收")
		}
		if err := transitionLot(ctx, tx, lot, domain.LotStatusRejected, actor, "reject",
			map[string]interface{}{"reason": reason}, nil, nil, nil, nil); err != nil {
			return err
		}
		out = lot
		return nil
	})
	return out, err
}
