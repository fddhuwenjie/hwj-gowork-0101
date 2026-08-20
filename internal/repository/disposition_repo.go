package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"metalmics/internal/domain"
)

// DispositionRepo 是异常处置单（让步接收/隔离处置）的持久化仓库。
type DispositionRepo struct {
	db DBTX
}

// NewDispositionRepo 构造处置单仓库。
func NewDispositionRepo(db DBTX) *DispositionRepo {
	return &DispositionRepo{db: db}
}

const dispositionColumns = `id, lot_id, type, reason, status, resolution, proposed_by, approved_by, executed_by, created_at, updated_at, version`

// Insert 插入处置单；同批次同类型存在未关闭单时冲突返回 Duplicate 供幂等处理。
func (r *DispositionRepo) Insert(ctx context.Context, d *domain.Disposition) error {
	now := nowUTC()
	d.CreatedAt, d.UpdatedAt = now, now
	d.Version = 1
	if d.Status == "" {
		d.Status = domain.DispositionProposed
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO dispositions (lot_id, type, reason, status, resolution, proposed_by, approved_by, executed_by, created_at, updated_at, version)
		 VALUES (?, ?, ?, ?, ?, ?, '', '', ?, ?, ?)`,
		d.LotID, string(d.Type), d.Reason, string(d.Status), d.Resolution, d.ProposedBy,
		timeToDB(d.CreatedAt), timeToDB(d.UpdatedAt), d.Version)
	if err != nil {
		if IsUniqueViolation(err) {
			return domain.Duplicate("disposition", fmt.Sprintf("lot=%d type=%s", d.LotID, d.Type))
		}
		return fmt.Errorf("插入处置单失败: %w", err)
	}
	d.ID, err = res.LastInsertId()
	return err
}

// GetByID 按主键查询处置单。
func (r *DispositionRepo) GetByID(ctx context.Context, id int64) (*domain.Disposition, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+dispositionColumns+` FROM dispositions WHERE id = ?`, id)
	return scanDisposition(row)
}

// GetOpenByLotType 查询批次某类型当前未关闭的处置单，无则返回 nil, nil。
func (r *DispositionRepo) GetOpenByLotType(ctx context.Context, lotID int64, dtype domain.DispositionType) (*domain.Disposition, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+dispositionColumns+` FROM dispositions
		 WHERE lot_id = ? AND type = ? AND status IN ('proposed', 'approved')
		 ORDER BY id DESC LIMIT 1`, lotID, string(dtype))
	return scanDisposition(row)
}

// HasApprovedConcession 判断批次是否存在已批准未执行的让步接收单。
func (r *DispositionRepo) HasApprovedConcession(ctx context.Context, lotID int64) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM dispositions WHERE lot_id = ? AND type = 'concession' AND status = 'approved'`,
		lotID).Scan(&n)
	return n > 0, err
}

func scanDisposition(row *sql.Row) (*domain.Disposition, error) {
	var d domain.Disposition
	var created, updated string
	if err := row.Scan(&d.ID, &d.LotID, &d.Type, &d.Reason, &d.Status, &d.Resolution,
		&d.ProposedBy, &d.ApprovedBy, &d.ExecutedBy, &created, &updated, &d.Version); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	var err error
	if d.CreatedAt, err = dbToTime(created); err != nil {
		return nil, err
	}
	if d.UpdatedAt, err = dbToTime(updated); err != nil {
		return nil, err
	}
	return &d, nil
}

// UpdateStatus 更新处置单状态（可同时写入批准人/执行人/执行方式），带乐观锁校验。
func (r *DispositionRepo) UpdateStatus(ctx context.Context, id int64, expectedVersion int64,
	status domain.DispositionStatus, resolution, approvedBy, executedBy *string) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE dispositions SET status = ?,
			resolution  = COALESCE(?, resolution),
			approved_by = COALESCE(?, approved_by),
			executed_by = COALESCE(?, executed_by),
			updated_at = ?, version = version + 1
		 WHERE id = ? AND version = ?`,
		string(status), resolution, approvedBy, executedBy, timeToDB(nowUTC()), id, expectedVersion)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// List 分页查询处置单，支持批次/类型/状态过滤、稳定排序。
func (r *DispositionRepo) List(ctx context.Context, f domain.DispositionFilter, p domain.PageRequest) (domain.Page[domain.Disposition], error) {
	where := []string{"1=1"}
	args := []interface{}{}
	if f.LotID > 0 {
		where = append(where, `lot_id = ?`)
		args = append(args, f.LotID)
	}
	if f.Type != "" {
		where = append(where, `type = ?`)
		args = append(args, string(f.Type))
	}
	if f.Status != "" {
		where = append(where, `status = ?`)
		args = append(args, string(f.Status))
	}
	cond := strings.Join(where, " AND ")
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM dispositions WHERE `+cond, args...).Scan(&total); err != nil {
		return domain.Page[domain.Disposition]{}, err
	}
	sortCol := map[string]string{"id": "id", "created_at": "created_at", "status": "status"}[p.Sort]
	query := fmt.Sprintf(
		`SELECT %s FROM dispositions WHERE %s ORDER BY %s %s, id ASC LIMIT ? OFFSET ?`,
		dispositionColumns, cond, sortCol, p.Order)
	rows, err := r.db.QueryContext(ctx, query, append(args, p.PageSize, p.Offset())...)
	if err != nil {
		return domain.Page[domain.Disposition]{}, err
	}
	defer rows.Close()
	items := []domain.Disposition{}
	for rows.Next() {
		var d domain.Disposition
		var created, updated string
		if err := rows.Scan(&d.ID, &d.LotID, &d.Type, &d.Reason, &d.Status, &d.Resolution,
			&d.ProposedBy, &d.ApprovedBy, &d.ExecutedBy, &created, &updated, &d.Version); err != nil {
			return domain.Page[domain.Disposition]{}, err
		}
		if d.CreatedAt, err = dbToTime(created); err != nil {
			return domain.Page[domain.Disposition]{}, err
		}
		if d.UpdatedAt, err = dbToTime(updated); err != nil {
			return domain.Page[domain.Disposition]{}, err
		}
		items = append(items, d)
	}
	if err := rows.Err(); err != nil {
		return domain.Page[domain.Disposition]{}, err
	}
	return domain.NewPage(items, total, p), nil
}
