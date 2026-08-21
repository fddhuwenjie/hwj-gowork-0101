package service

import (
	"context"
	"database/sql"

	"metalmics/internal/domain"
	"metalmics/internal/repository"
)

// ExceptionService 承载“异常处置”流程：隔离处置与让步接收。
// 处置单与批次状态机共享数据并相互制约：
// 隔离使批次脱离主流程，让步接收使不符合批次获得接收资格。
type ExceptionService struct {
	store *Store
}

// NewExceptionService 构造异常处置服务。
func NewExceptionService(store *Store) *ExceptionService {
	return &ExceptionService{store: store}
}

// ProposeDisposition 提出处置单（事务：处置单入库 + 批次状态联动 + 审计）。
//
// quarantine：批次处于任意非终态即可提出，提出后批次立即进入 quarantined；
// concession：R10 仅允许对最终结论为 fail 的批次提出，不改变批次状态。
// 同批次同类型仅允许一张未关闭处置单，重复提交返回既有单（幂等）。
func (s *ExceptionService) ProposeDisposition(ctx context.Context, d *domain.Disposition, actor string) (created bool, err error) {
	if err := d.Validate(); err != nil {
		return false, err
	}
	err = s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		dispRepo := repository.NewDispositionRepo(tx)
		if err := dispRepo.Insert(ctx, d); err != nil {
			if !domain.IsCode(err, domain.ErrCodeDuplicate) {
				return err
			}
			existing, gerr := dispRepo.GetOpenByLotType(ctx, d.LotID, d.Type)
			if gerr != nil {
				return gerr
			}
			if existing == nil {
				return err
			}
			*d = *existing
			return nil
		}
		lot, err := loadLot(ctx, tx, d.LotID, 0)
		if err != nil {
			return err
		}
		switch d.Type {
		case domain.DispositionQuarantine:
			if err := transitionLot(ctx, tx, lot, domain.LotStatusQuarantined, actor, "quarantine",
				map[string]interface{}{"disposition_id": d.ID, "reason": d.Reason}, nil, nil, nil, nil); err != nil {
				return err
			}
		case domain.DispositionConcession:
			if lot.Status != domain.LotStatusJudged && lot.Status != domain.LotStatusQuarantined {
				return domain.InvalidTransition("material_lot", string(lot.Status), "concession")
			}
			if err := domain.RequireConcessionForFailure(lot); err != nil {
				return err
			}
		}
		created = true
		return audit(ctx, tx, "disposition", d.ID, "propose", actor,
			map[string]interface{}{"lot_id": d.LotID, "type": d.Type})
	})
	return created, err
}

// ApproveDisposition 批准处置单（事务：proposed->approved + 审计）。
// 批准人不能与提出人为同一人。
func (s *ExceptionService) ApproveDisposition(ctx context.Context, id, expectedVersion int64, approver string) (*domain.Disposition, error) {
	if approver == "" {
		return nil, domain.Validation("approved_by", "批准人不能为空")
	}
	var out *domain.Disposition
	err := s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		d, err := s.loadDisposition(ctx, tx, id, expectedVersion)
		if err != nil {
			return err
		}
		if d.Status != domain.DispositionProposed {
			return domain.InvalidTransition("disposition", string(d.Status), string(domain.DispositionApproved))
		}
		if approver == d.ProposedBy {
			return domain.RuleViolation(domain.RuleOverrideRequiresCoDecision,
				"处置单批准人不能与提出人为同一人")
		}
		ok, err := repository.NewDispositionRepo(tx).UpdateStatus(ctx, id, d.Version,
			domain.DispositionApproved, nil, &approver, nil)
		if err != nil {
			return err
		}
		if !ok {
			return domain.VersionConflict("disposition", id, d.Version, -1)
		}
		if err := audit(ctx, tx, "disposition", id, "approve", approver, nil); err != nil {
			return err
		}
		d.Status = domain.DispositionApproved
		d.ApprovedBy = approver
		d.Version++
		out = d
		return nil
	})
	return out, err
}

// ExecuteDisposition 执行处置单（事务：approved->executed + 批次终态流转 + 审计）。
//
// 执行方式 resolution：
//   - scrap / return_to_supplier：批次 quarantined->rejected；
//   - concession_accept：需存在同批次已批准的让步接收单，批次 ->accepted；
//   - urgent_release：紧急放行，批次 quarantined->accepted（无结论无证明，纳入缺失证明统计）。
func (s *ExceptionService) ExecuteDisposition(ctx context.Context, id, expectedVersion int64,
	executor, resolution string) (*domain.Disposition, error) {
	if executor == "" {
		return nil, domain.Validation("executed_by", "执行人不能为空")
	}
	var out *domain.Disposition
	err := s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		d, err := s.loadDisposition(ctx, tx, id, expectedVersion)
		if err != nil {
			return err
		}
		if d.Status != domain.DispositionApproved {
			return domain.InvalidTransition("disposition", string(d.Status), string(domain.DispositionExecuted))
		}
		dispRepo := repository.NewDispositionRepo(tx)
		lot, err := loadLot(ctx, tx, d.LotID, 0)
		if err != nil {
			return err
		}
		switch resolution {
		case "scrap", "return_to_supplier":
			if d.Type != domain.DispositionQuarantine {
				return domain.Validation("resolution", "该执行方式仅适用于隔离处置单")
			}
			if err := transitionLot(ctx, tx, lot, domain.LotStatusRejected, executor, "dispose_reject",
				map[string]interface{}{"disposition_id": d.ID, "resolution": resolution}, nil, nil, nil, nil); err != nil {
				return err
			}
		case "concession_accept":
			has, err := dispRepo.HasActionableConcession(ctx, d.LotID)
			if err != nil {
				return err
			}
			if !has {
				return domain.RuleViolation(domain.RuleAcceptRequiresPassOrConcession,
					"不存在已批准的让步接收单，不允许让步接收执行")
			}
			now := nowTime()
			if err := transitionLot(ctx, tx, lot, domain.LotStatusAccepted, executor, "concession_accept",
				map[string]interface{}{"disposition_id": d.ID}, nil, nil, &executor, &now); err != nil {
				return err
			}
		case "urgent_release":
			if d.Type != domain.DispositionQuarantine {
				return domain.Validation("resolution", "紧急放行仅适用于隔离处置单")
			}
			now := nowTime()
			if err := transitionLot(ctx, tx, lot, domain.LotStatusAccepted, executor, "urgent_release",
				map[string]interface{}{"disposition_id": d.ID}, nil, nil, &executor, &now); err != nil {
				return err
			}
		default:
			return domain.Validation("resolution", "执行方式仅支持 scrap/return_to_supplier/concession_accept/urgent_release")
		}
		ok, err := dispRepo.UpdateStatus(ctx, id, d.Version, domain.DispositionExecuted,
			&resolution, nil, &executor)
		if err != nil {
			return err
		}
		if !ok {
			return domain.VersionConflict("disposition", id, d.Version, -1)
		}
		if err := audit(ctx, tx, "disposition", id, "execute", executor,
			map[string]interface{}{"resolution": resolution}); err != nil {
			return err
		}
		d.Status = domain.DispositionExecuted
		d.Resolution = resolution
		d.ExecutedBy = executor
		d.Version++
		out = d
		return nil
	})
	return out, err
}

// ListDispositions 分页查询处置单。
func (s *ExceptionService) ListDispositions(ctx context.Context, f domain.DispositionFilter, p domain.PageRequest) (domain.Page[domain.Disposition], error) {
	return repository.NewDispositionRepo(s.store.DB()).List(ctx, f, p)
}

// loadDisposition 在事务内加载处置单并校验存在性与版本。
func (s *ExceptionService) loadDisposition(ctx context.Context, tx *sql.Tx, id, expectedVersion int64) (*domain.Disposition, error) {
	d, err := repository.NewDispositionRepo(tx).GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, domain.NotFound("disposition", id)
	}
	if expectedVersion > 0 && d.Version != expectedVersion {
		return nil, domain.VersionConflict("disposition", id, expectedVersion, d.Version)
	}
	return d, nil
}
