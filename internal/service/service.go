// Package service 承载业务编排：日常作业、异常处置、复核归档三条流程
// 共享同一批持久化实体并相互制约。所有多实体写入在单事务内完成，
// 每个改变状态的操作都会追加审计事件。
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"metalmics/internal/domain"
	"metalmics/internal/repository"
)

// Store 聚合数据库句柄与事务管理器，为各服务提供统一入口。
type Store struct {
	db *sql.DB
	tx *repository.TxManager
}

// NewStore 构造服务层存储门面。
func NewStore(db *sql.DB) *Store {
	return &Store{db: db, tx: repository.NewTxManager(db)}
}

// DB 返回底层连接池。
func (s *Store) DB() *sql.DB {
	return s.db
}

// Tx 返回事务管理器。
func (s *Store) Tx() *repository.TxManager {
	return s.tx
}

// audit 在当前事务内追加审计事件。detail 会被序列化为 JSON。
func audit(ctx context.Context, tx *sql.Tx, entity string, entityID int64, action, actor string, detail interface{}) error {
	if actor == "" {
		actor = "system"
	}
	payload := "{}"
	if detail != nil {
		b, err := json.Marshal(detail)
		if err != nil {
			return err
		}
		payload = string(b)
	}
	event := &domain.AuditEvent{
		Entity: entity, EntityID: entityID, Action: action, Actor: actor, Detail: payload,
	}
	return repository.NewAuditRepo(tx).Insert(ctx, event)
}

// loadLot 在事务内加载批次并校验存在性与乐观锁版本。
// expectedVersion <= 0 表示不校验版本。
func loadLot(ctx context.Context, tx *sql.Tx, id, expectedVersion int64) (*domain.MaterialLot, error) {
	lot, err := repository.NewLotRepo(tx).GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if lot == nil {
		return nil, domain.NotFound("material_lot", id)
	}
	if expectedVersion > 0 && lot.Version != expectedVersion {
		return nil, domain.VersionConflict("material_lot", id, expectedVersion, lot.Version)
	}
	return lot, nil
}

// transitionLot 在事务内执行批次状态流转：状态机校验 + 乐观锁更新 + 审计。
// 可选字段（初检/复验结论、接收人、接收时间）为 nil 时不更新。
func transitionLot(ctx context.Context, tx *sql.Tx, lot *domain.MaterialLot, next domain.LotStatus,
	actor, action string, detail interface{},
	initialResult, retestResult, acceptedBy *string, acceptedAt *time.Time) error {
	if err := lot.Status.MustTransitionTo(next); err != nil {
		return err
	}
	ok, err := repository.NewLotRepo(tx).Transition(ctx, lot.ID, lot.Version, next,
		initialResult, retestResult, acceptedBy, acceptedAt)
	if err != nil {
		return err
	}
	if !ok {
		return domain.VersionConflict("material_lot", lot.ID, lot.Version, -1)
	}
	if err := audit(ctx, tx, "material_lot", lot.ID, action, actor, detail); err != nil {
		return err
	}
	lot.Status = next
	lot.Version++
	// 同步可选字段到内存对象，保证调用方拿到的批次与已落库数据一致
	if initialResult != nil {
		lot.InitialResult = *initialResult
	}
	if retestResult != nil {
		lot.RetestResult = *retestResult
	}
	if acceptedBy != nil {
		lot.AcceptedBy = *acceptedBy
	}
	if acceptedAt != nil {
		lot.AcceptedAt = acceptedAt
	}
	return nil
}

// strPtr 返回字符串指针的便捷函数。
func strPtr(s string) *string {
	return &s
}

// nowTime 返回当前 UTC 时间，供接收时间等业务字段使用。
func nowTime() time.Time {
	return time.Now().UTC().Round(0)
}
