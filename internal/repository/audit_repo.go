package repository

import (
	"context"
	"fmt"
	"strings"

	"metalmics/internal/domain"
)

// AuditRepo 是审计记录的持久化仓库。审计事件只增不改，
// 必须与业务写入处于同一事务。
type AuditRepo struct {
	db DBTX
}

// NewAuditRepo 构造审计仓库。
func NewAuditRepo(db DBTX) *AuditRepo {
	return &AuditRepo{db: db}
}

// Insert 追加一条审计事件。
func (r *AuditRepo) Insert(ctx context.Context, e *domain.AuditEvent) error {
	e.CreatedAt = nowUTC()
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO audit_events (entity, entity_id, action, actor, detail, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		e.Entity, e.EntityID, e.Action, e.Actor, e.Detail, timeToDB(e.CreatedAt))
	if err != nil {
		return fmt.Errorf("写入审计事件失败: %w", err)
	}
	e.ID, err = res.LastInsertId()
	return err
}

// List 分页查询审计事件，支持实体/操作人/起始时间过滤，
// 默认按 id 升序（事件发生顺序）稳定排列。
func (r *AuditRepo) List(ctx context.Context, f domain.AuditFilter, p domain.PageRequest) (domain.Page[domain.AuditEvent], error) {
	where := []string{"1=1"}
	args := []interface{}{}
	if f.Entity != "" {
		where = append(where, `entity = ?`)
		args = append(args, f.Entity)
	}
	if f.EntityID > 0 {
		where = append(where, `entity_id = ?`)
		args = append(args, f.EntityID)
	}
	if f.Actor != "" {
		where = append(where, `actor = ?`)
		args = append(args, f.Actor)
	}
	if f.Since != nil {
		where = append(where, `created_at >= ?`)
		args = append(args, timeToDB(*f.Since))
	}
	cond := strings.Join(where, " AND ")
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE `+cond, args...).Scan(&total); err != nil {
		return domain.Page[domain.AuditEvent]{}, err
	}
	sortCol := map[string]string{"id": "id", "created_at": "created_at", "entity": "entity"}[p.Sort]
	query := fmt.Sprintf(
		`SELECT id, entity, entity_id, action, actor, detail, created_at FROM audit_events
		 WHERE %s ORDER BY %s %s, id DESC LIMIT ? OFFSET ?`, cond, sortCol, p.Order)
	rows, err := r.db.QueryContext(ctx, query, append(args, p.PageSize, p.Offset())...)
	if err != nil {
		return domain.Page[domain.AuditEvent]{}, err
	}
	defer rows.Close()
	items := []domain.AuditEvent{}
	for rows.Next() {
		var e domain.AuditEvent
		var created string
		if err := rows.Scan(&e.ID, &e.Entity, &e.EntityID, &e.Action, &e.Actor, &e.Detail, &created); err != nil {
			return domain.Page[domain.AuditEvent]{}, err
		}
		t, err := dbToTime(created)
		if err != nil {
			return domain.Page[domain.AuditEvent]{}, err
		}
		e.CreatedAt = t
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		return domain.Page[domain.AuditEvent]{}, err
	}
	return domain.NewPage(items, total, p), nil
}
