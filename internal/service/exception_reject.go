package service

import (
	"context"
	"database/sql"

	"metalmics/internal/domain"
	"metalmics/internal/repository"
)

// RejectDisposition 驳回处置单（事务：proposed->rejected + 审计）。
// 驳回隔离处置单不会自动解除批次隔离状态，需要重新提出处置或复验。
func (s *ExceptionService) RejectDisposition(ctx context.Context, id, expectedVersion int64, approver string) (*domain.Disposition, error) {
	if approver == "" {
		return nil, domain.Validation("approved_by", "审批人不能为空")
	}
	var out *domain.Disposition
	err := s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		d, err := s.loadDisposition(ctx, tx, id, expectedVersion)
		if err != nil {
			return err
		}
		if d.Status != domain.DispositionProposed {
			return domain.InvalidTransition("disposition", string(d.Status), string(domain.DispositionRejected))
		}
		if approver == d.ProposedBy {
			return domain.RuleViolation(domain.RuleOverrideRequiresCoDecision,
				"处置单审批人不能与提出人为同一人")
		}
		ok, err := repository.NewDispositionRepo(tx).UpdateStatus(ctx, id, d.Version,
			domain.DispositionRejected, nil, &approver, nil)
		if err != nil {
			return err
		}
		if !ok {
			return domain.VersionConflict("disposition", id, d.Version, -1)
		}
		if err := audit(ctx, tx, "disposition", id, "reject", approver, nil); err != nil {
			return err
		}
		// 驳回是处置单的终止态：返回对象必须与落库的 rejected 一致，
		// 切断后续 execute 等入口对该单“已批准”的误判。
		d.Status = domain.DispositionRejected
		d.ApprovedBy = approver
		d.Version++
		out = d
		return nil
	})
	return out, err
}
