package service

import (
	"context"
	"database/sql"

	"metalmics/internal/domain"
	"metalmics/internal/repository"
)

// RejectRetest 驳回复验申请（事务：open->rejected + 批次 retesting->judged 回退 + 审计）。
// 驳回后批次回到 judged 状态，维持初检结论。
func (s *ReviewService) RejectRetest(ctx context.Context, taskID, expectedVersion int64, approver string) (*domain.RetestTask, error) {
	if approver == "" {
		return nil, domain.Validation("approved_by", "审批人不能为空")
	}
	var out *domain.RetestTask
	err := s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		task, err := s.loadTask(ctx, tx, taskID, expectedVersion)
		if err != nil {
			return err
		}
		if task.Status != domain.RetestStatusOpen {
			return domain.InvalidTransition("retest_task", string(task.Status), string(domain.RetestStatusRejected))
		}
		lot, err := loadLot(ctx, tx, task.LotID, 0)
		if err != nil {
			return err
		}
		if lot.Status == domain.LotStatusRetesting {
			if err := transitionLot(ctx, tx, lot, domain.LotStatusJudged, approver, "reject_retest",
				map[string]interface{}{"task_id": task.ID}, nil, nil, nil, nil); err != nil {
				return err
			}
		}
		ok, err := repository.NewRetestRepo(tx).UpdateStatus(ctx, taskID, task.Version,
			domain.RetestStatusRejected, &approver)
		if err != nil {
			return err
		}
		if !ok {
			return domain.VersionConflict("retest_task", taskID, task.Version, -1)
		}
		if err := audit(ctx, tx, "retest_task", taskID, "reject", approver, nil); err != nil {
			return err
		}
		task.Status = domain.RetestStatusRejected
		task.ApprovedBy = approver
		task.Version++
		out = task
		return nil
	})
	return out, err
}
