package repository

import (
	"context"
	"database/sql"
)

// DBTX 抽象 *sql.DB 与 *sql.Tx，使 Repository 方法既可在独立连接
// 也可在事务上下文中执行，是多实体事务写入的基础。
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// TxManager 管理事务边界。服务层通过 InTx 把多个 Repository 调用
// 编排进同一事务，任一失败即整体回滚。
type TxManager struct {
	db *sql.DB
}

// NewTxManager 构造事务管理器。
func NewTxManager(db *sql.DB) *TxManager {
	return &TxManager{db: db}
}

// DB 返回底层连接池（用于只读查询仓库的构造）。
func (m *TxManager) DB() *sql.DB {
	return m.db
}

// InTx 在单事务内执行 fn。fn 返回错误则回滚，否则提交；
// panic 同样触发回滚并继续抛出。
func (m *TxManager) InTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
