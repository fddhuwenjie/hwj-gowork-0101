package service

import (
	"context"
	"database/sql"

	"metalmics/internal/domain"
	"metalmics/internal/repository"
)

// DailyService 承载“日常作业”流程：来料登记、取样制样、光谱分析、
// 材质证明核对、符合性判定与接收确认。
type DailyService struct {
	store *Store
}

// NewDailyService 构造日常作业服务。
func NewDailyService(store *Store) *DailyService {
	return &DailyService{store: store}
}

// RegisterSupplier 登记供方。以 code 作为幂等键：重复提交返回既有记录与 created=false。
func (s *DailyService) RegisterSupplier(ctx context.Context, sup *domain.Supplier) (created bool, err error) {
	if err := sup.Validate(); err != nil {
		return false, err
	}
	err = s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		repo := repository.NewSupplierRepo(tx)
		if err := repo.Insert(ctx, sup); err != nil {
			if !domain.IsCode(err, domain.ErrCodeDuplicate) {
				return err
			}
			existing, gerr := repo.GetByCode(ctx, sup.Code)
			if gerr != nil {
				return gerr
			}
			*sup = *existing
			return nil
		}
		created = true
		return audit(ctx, tx, "supplier", sup.ID, "register", "system", map[string]string{"code": sup.Code})
	})
	return created, err
}

// ListSuppliers 分页查询供方。
func (s *DailyService) ListSuppliers(ctx context.Context, f domain.SupplierFilter, p domain.PageRequest) (domain.Page[domain.Supplier], error) {
	return repository.NewSupplierRepo(s.store.DB()).List(ctx, f, p)
}

// GetSupplier 查询供方详情。
func (s *DailyService) GetSupplier(ctx context.Context, id int64) (*domain.Supplier, error) {
	sup, err := repository.NewSupplierRepo(s.store.DB()).GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if sup == nil {
		return nil, domain.NotFound("supplier", id)
	}
	return sup, nil
}

// CreateGradeRule 创建牌号规则版本（draft）。以 (grade, version_no) 作为幂等键。
func (s *DailyService) CreateGradeRule(ctx context.Context, rule *domain.GradeRule, actor string) (created bool, err error) {
	if err := rule.Validate(); err != nil {
		return false, err
	}
	err = s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		repo := repository.NewGradeRuleRepo(tx)
		if err := repo.Insert(ctx, rule); err != nil {
			if !domain.IsCode(err, domain.ErrCodeDuplicate) {
				return err
			}
			existing, gerr := repo.GetByGradeVersion(ctx, rule.Grade, rule.VersionNo)
			if gerr != nil {
				return gerr
			}
			*rule = *existing
			return nil
		}
		created = true
		return audit(ctx, tx, "grade_rule", rule.ID, "create", actor,
			map[string]interface{}{"grade": rule.Grade, "version_no": rule.VersionNo})
	})
	return created, err
}

// ListGradeRules 分页查询牌号规则。
func (s *DailyService) ListGradeRules(ctx context.Context, f domain.GradeRuleFilter, p domain.PageRequest) (domain.Page[domain.GradeRule], error) {
	return repository.NewGradeRuleRepo(s.store.DB()).List(ctx, f, p)
}

// ActivateGradeRule 激活规则版本：draft->active，同事务废止同牌号旧 active 版本。
// 激活使新版本立即成为判定依据，属于复核归档流程与日常流程的共享制约点。
func (s *DailyService) ActivateGradeRule(ctx context.Context, id, expectedVersion int64, actor string) (*domain.GradeRule, error) {
	var out *domain.GradeRule
	err := s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		repo := repository.NewGradeRuleRepo(tx)
		rule, err := repo.GetByID(ctx, id)
		if err != nil {
			return err
		}
		if rule == nil {
			return domain.NotFound("grade_rule", id)
		}
		if expectedVersion > 0 && rule.Version != expectedVersion {
			return domain.VersionConflict("grade_rule", id, expectedVersion, rule.Version)
		}
		if !rule.Status.CanTransitionTo(domain.RuleStatusActive) {
			return domain.InvalidTransition("grade_rule", string(rule.Status), string(domain.RuleStatusActive))
		}
		if err := repo.RetireActiveByGrade(ctx, rule.Grade); err != nil {
			return err
		}
		ok, err := repo.UpdateStatus(ctx, id, domain.RuleStatusActive, rule.Version)
		if err != nil {
			return err
		}
		if !ok {
			return domain.VersionConflict("grade_rule", id, rule.Version, -1)
		}
		if err := audit(ctx, tx, "grade_rule", id, "activate", actor,
			map[string]interface{}{"grade": rule.Grade, "version_no": rule.VersionNo}); err != nil {
			return err
		}
		rule.Status = domain.RuleStatusActive
		rule.Version++
		out = rule
		return nil
	})
	return out, err
}

// RetireGradeRule 废止规则版本：draft/active->retired。
func (s *DailyService) RetireGradeRule(ctx context.Context, id, expectedVersion int64, actor string) (*domain.GradeRule, error) {
	var out *domain.GradeRule
	err := s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		repo := repository.NewGradeRuleRepo(tx)
		rule, err := repo.GetByID(ctx, id)
		if err != nil {
			return err
		}
		if rule == nil {
			return domain.NotFound("grade_rule", id)
		}
		if expectedVersion > 0 && rule.Version != expectedVersion {
			return domain.VersionConflict("grade_rule", id, expectedVersion, rule.Version)
		}
		if !rule.Status.CanTransitionTo(domain.RuleStatusRetired) {
			return domain.InvalidTransition("grade_rule", string(rule.Status), string(domain.RuleStatusRetired))
		}
		ok, err := repo.UpdateStatus(ctx, id, domain.RuleStatusRetired, rule.Version)
		if err != nil {
			return err
		}
		if !ok {
			return domain.VersionConflict("grade_rule", id, rule.Version, -1)
		}
		if err := audit(ctx, tx, "grade_rule", id, "retire", actor, nil); err != nil {
			return err
		}
		rule.Status = domain.RuleStatusRetired
		rule.Version++
		out = rule
		return nil
	})
	return out, err
}

// RegisterLot 来料登记（事务用例一：供方校验 + 规则校验 + 批次入库 + 审计）。
// R01 供方必须存在；R02 牌号必须有 active 规则版本。以 lot_no 作为幂等键。
func (s *DailyService) RegisterLot(ctx context.Context, lot *domain.MaterialLot, actor string) (created bool, err error) {
	if err := lot.Validate(); err != nil {
		return false, err
	}
	err = s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		lotRepo := repository.NewLotRepo(tx)
		if err := lotRepo.Insert(ctx, lot); err != nil {
			if !domain.IsCode(err, domain.ErrCodeDuplicate) {
				return err
			}
			existing, gerr := lotRepo.GetByLotNo(ctx, lot.LotNo)
			if gerr != nil {
				return gerr
			}
			*lot = *existing
			return nil
		}
		sup, err := repository.NewSupplierRepo(tx).GetByID(ctx, lot.SupplierID)
		if err != nil {
			return err
		}
		if err := domain.RequireSupplierExists(sup); err != nil {
			return err
		}
		rule, err := repository.NewGradeRuleRepo(tx).GetActiveByGrade(ctx, lot.Grade)
		if err != nil {
			return err
		}
		if err := domain.RequireActiveGradeRule(rule, lot.Grade); err != nil {
			return err
		}
		created = true
		return audit(ctx, tx, "material_lot", lot.ID, "register", actor,
			map[string]interface{}{"lot_no": lot.LotNo, "heat_no": lot.HeatNo, "grade": lot.Grade})
	})
	return created, err
}

// ListLots 分页过滤查询批次。
func (s *DailyService) ListLots(ctx context.Context, f domain.LotFilter, p domain.PageRequest) (domain.Page[domain.MaterialLot], error) {
	return repository.NewLotRepo(s.store.DB()).List(ctx, f, p)
}

// GetLot 查询批次详情。
func (s *DailyService) GetLot(ctx context.Context, id int64) (*domain.MaterialLot, error) {
	lot, err := repository.NewLotRepo(s.store.DB()).GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if lot == nil {
		return nil, domain.NotFound("material_lot", id)
	}
	return lot, nil
}

// CreateSamplingPlan 制定取样计划（事务：计划入库 + 审计）。
// 批次必须处于 registered；每批次仅一份计划。以 plan_no 作为幂等键。
func (s *DailyService) CreateSamplingPlan(ctx context.Context, plan *domain.SamplingPlan, actor string) (created bool, err error) {
	if err := plan.Validate(); err != nil {
		return false, err
	}
	created, err = repository.NewSamplingPlanRepo(s.store.DB()).CreateAhead(ctx, plan)
	if err != nil || !created {
		return created, err
	}
	err = s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		lot, err := loadLot(ctx, tx, plan.LotID, 0)
		if err != nil {
			return err
		}
		if lot.Status != domain.LotStatusRegistered {
			return domain.InvalidTransition("material_lot", string(lot.Status), "sampling_plan")
		}
		return audit(ctx, tx, "sampling_plan", plan.ID, "create", actor,
			map[string]interface{}{"lot_id": plan.LotID, "required_count": plan.RequiredCount})
	})
	return created, err
}

// RegisterSamples 批量登记样本（事务：样本入库 + 审计，任一失败整体回滚）。
// 计划必须 active；单批样本数不得超过剩余应取样数量。
// 已存在的 (plan, sample_no) 视为幂等重放并跳过。
func (s *DailyService) RegisterSamples(ctx context.Context, planID int64, samples []*domain.Sample, actor string) (inserted int, err error) {
	if len(samples) == 0 {
		return 0, domain.Validation("samples", "样本列表不能为空")
	}
	err = s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		planRepo := repository.NewSamplingPlanRepo(tx)
		sampleRepo := repository.NewSampleRepo(tx)
		plan, err := planRepo.GetByID(ctx, planID)
		if err != nil {
			return err
		}
		if plan == nil {
			return domain.NotFound("sampling_plan", planID)
		}
		if plan.Status != domain.PlanStatusActive {
			return domain.InvalidTransition("sampling_plan", string(plan.Status), "register_samples")
		}
		existing, err := sampleRepo.CountByPlanAndKind(ctx, planID, domain.SampleKindInitial)
		if err != nil {
			return err
		}
		inserted = 0
		for _, smp := range samples {
			smp.PlanID = planID
			smp.Kind = domain.SampleKindInitial
			if err := smp.Validate(); err != nil {
				return err
			}
			if existing+inserted >= plan.RequiredCount {
				return domain.RuleViolation(domain.RuleSampleCountComplete,
					"登记样本数超过计划应取样数量")
			}
			if err := sampleRepo.Insert(ctx, smp); err != nil {
				if !domain.IsCode(err, domain.ErrCodeDuplicate) {
					return err
				}
				continue // 幂等重放：跳过已存在样本
			}
			inserted++
		}
		return audit(ctx, tx, "sampling_plan", planID, "register_samples", actor,
			map[string]interface{}{"inserted": inserted})
	})
	return inserted, err
}

// CompleteSampling 取样完成（事务用例二：R06 数量校验 + 计划完成 + 批次 registered->sampled + 审计）。
func (s *DailyService) CompleteSampling(ctx context.Context, lotID, expectedVersion int64, actor string) (*domain.MaterialLot, error) {
	var out *domain.MaterialLot
	err := s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		lot, err := loadLot(ctx, tx, lotID, expectedVersion)
		if err != nil {
			return err
		}
		planRepo := repository.NewSamplingPlanRepo(tx)
		plan, err := planRepo.GetByLot(ctx, lotID)
		if err != nil {
			return err
		}
		if plan == nil {
			return domain.RuleViolation(domain.RuleSampleCountComplete, "批次尚无取样计划")
		}
		if plan.Status != domain.PlanStatusActive {
			return domain.InvalidTransition("sampling_plan", string(plan.Status), string(domain.PlanStatusCompleted))
		}
		count, err := repository.NewSampleRepo(tx).CountByPlanAndKind(ctx, plan.ID, domain.SampleKindInitial)
		if err != nil {
			return err
		}
		if err := domain.RequireSampleCountComplete(plan, count); err != nil {
			return err
		}
		ok, err := planRepo.UpdateStatus(ctx, plan.ID, plan.Version, domain.PlanStatusCompleted)
		if err != nil {
			return err
		}
		if !ok {
			return domain.VersionConflict("sampling_plan", plan.ID, plan.Version, -1)
		}
		if err := transitionLot(ctx, tx, lot, domain.LotStatusSampled, actor, "complete_sampling",
			map[string]interface{}{ "plan_no": plan.PlanNo, "samples": count }, nil, nil, nil, nil); err != nil {
			return err
		}
		out = lot
		return nil
	})
	return out, err
}
