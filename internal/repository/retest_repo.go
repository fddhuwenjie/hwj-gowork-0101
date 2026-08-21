package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"metalmics/internal/domain"
)

// RetestRepo 是复验任务的持久化仓库。
type RetestRepo struct {
	db DBTX
}

// NewRetestRepo 构造复验任务仓库。
func NewRetestRepo(db DBTX) *RetestRepo {
	return &RetestRepo{db: db}
}

const retestColumns = `id, lot_id, sample_id, reason, status, requested_by, approved_by, created_at, updated_at, version`

// Insert 插入复验任务；同批次存在未关闭任务时冲突返回 Duplicate 供幂等处理。
func (r *RetestRepo) Insert(ctx context.Context, t *domain.RetestTask) error {
	now := nowUTC()
	t.CreatedAt, t.UpdatedAt = now, now
	t.Version = 1
	if t.Status == "" {
		t.Status = domain.RetestStatusOpen
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO retest_tasks (lot_id, sample_id, reason, status, requested_by, approved_by, created_at, updated_at, version)
		 VALUES (?, ?, ?, ?, ?, '', ?, ?, ?)`,
		t.LotID, t.SampleID, t.Reason, string(t.Status), t.RequestedBy,
		timeToDB(t.CreatedAt), timeToDB(t.UpdatedAt), t.Version)
	if err != nil {
		if IsUniqueViolation(err) {
			return domain.Duplicate("retest_task", fmt.Sprintf("lot=%d", t.LotID))
		}
		return fmt.Errorf("插入复验任务失败: %w", err)
	}
	t.ID, err = res.LastInsertId()
	return err
}

// GetByID 按主键查询复验任务。
func (r *RetestRepo) GetByID(ctx context.Context, id int64) (*domain.RetestTask, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+retestColumns+` FROM retest_tasks WHERE id = ?`, id)
	return scanRetest(row)
}

// GetOpenByLot 查询批次当前未关闭的复验任务，无则返回 nil, nil。
func (r *RetestRepo) GetOpenByLot(ctx context.Context, lotID int64) (*domain.RetestTask, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+retestColumns+` FROM retest_tasks WHERE lot_id = ? AND status IN ('open', 'approved')
		 ORDER BY id DESC LIMIT 1`, lotID)
	return scanRetest(row)
}

// GetRetainedCandidate checks target-plan existence and sample retention separately.
func (r *RetestRepo) GetRetainedCandidate(ctx context.Context, lotID, sampleID int64) (*domain.Sample, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+sampleColumns+` FROM samples
		 WHERE id = ? AND kind = 'initial' AND retained = 1
		   AND EXISTS (SELECT 1 FROM sampling_plans WHERE lot_id = ?)`, sampleID, lotID)
	return scanSample(row)
}

// GetApprovedBySample finds an approved task using only its sample identity.
func (r *RetestRepo) GetApprovedBySample(ctx context.Context, sampleID int64) (*domain.RetestTask, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+retestColumns+` FROM retest_tasks WHERE sample_id = ? AND status = 'approved'
		 ORDER BY id DESC LIMIT 1`, sampleID)
	return scanRetest(row)
}

func scanRetest(row *sql.Row) (*domain.RetestTask, error) {
	var t domain.RetestTask
	var created, updated string
	if err := row.Scan(&t.ID, &t.LotID, &t.SampleID, &t.Reason, &t.Status,
		&t.RequestedBy, &t.ApprovedBy, &created, &updated, &t.Version); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	var err error
	if t.CreatedAt, err = dbToTime(created); err != nil {
		return nil, err
	}
	if t.UpdatedAt, err = dbToTime(updated); err != nil {
		return nil, err
	}
	return &t, nil
}

// UpdateStatus 更新任务状态（可同时写入批准人），带乐观锁校验。
func (r *RetestRepo) UpdateStatus(ctx context.Context, id int64, expectedVersion int64,
	status domain.RetestStatus, approvedBy *string) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE retest_tasks SET status = ?, approved_by = COALESCE(?, approved_by),
			updated_at = ?, version = version + 1
		 WHERE id = ? AND version = ?`,
		string(status), approvedBy, timeToDB(nowUTC()), id, expectedVersion)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// List 分页查询复验任务，支持批次与状态过滤、稳定排序。
func (r *RetestRepo) List(ctx context.Context, f domain.RetestFilter, p domain.PageRequest) (domain.Page[domain.RetestTask], error) {
	where := []string{"1=1"}
	args := []interface{}{}
	if f.LotID > 0 {
		where = append(where, `lot_id = ?`)
		args = append(args, f.LotID)
	}
	if f.Status != "" {
		where = append(where, `status = ?`)
		args = append(args, string(f.Status))
	}
	cond := strings.Join(where, " AND ")
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM retest_tasks WHERE `+cond, args...).Scan(&total); err != nil {
		return domain.Page[domain.RetestTask]{}, err
	}
	sortCol := map[string]string{"id": "id", "created_at": "created_at", "status": "status"}[p.Sort]
	query := fmt.Sprintf(
		`SELECT %s FROM retest_tasks WHERE %s ORDER BY %s %s, id ASC LIMIT ? OFFSET ?`,
		retestColumns, cond, sortCol, p.Order)
	rows, err := r.db.QueryContext(ctx, query, append(args, p.PageSize, p.Offset())...)
	if err != nil {
		return domain.Page[domain.RetestTask]{}, err
	}
	defer rows.Close()
	items := []domain.RetestTask{}
	for rows.Next() {
		var t domain.RetestTask
		var created, updated string
		if err := rows.Scan(&t.ID, &t.LotID, &t.SampleID, &t.Reason, &t.Status,
			&t.RequestedBy, &t.ApprovedBy, &created, &updated, &t.Version); err != nil {
			return domain.Page[domain.RetestTask]{}, err
		}
		if t.CreatedAt, err = dbToTime(created); err != nil {
			return domain.Page[domain.RetestTask]{}, err
		}
		if t.UpdatedAt, err = dbToTime(updated); err != nil {
			return domain.Page[domain.RetestTask]{}, err
		}
		items = append(items, t)
	}
	if err := rows.Err(); err != nil {
		return domain.Page[domain.RetestTask]{}, err
	}
	return domain.NewPage(items, total, p), nil
}
