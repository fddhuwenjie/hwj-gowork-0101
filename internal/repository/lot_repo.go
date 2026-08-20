package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"metalmics/internal/domain"
)

// LotRepo 是来料批次的持久化仓库。
type LotRepo struct {
	db DBTX
}

// NewLotRepo 构造来料批次仓库。
func NewLotRepo(db DBTX) *LotRepo {
	return &LotRepo{db: db}
}

const lotColumns = `id, lot_no, supplier_id, heat_no, grade, quantity, status,
	initial_result, retest_result, accepted_by, accepted_at, received_at, created_at, updated_at, version`

// Insert 插入来料批次；lot_no 冲突返回 Duplicate 供幂等处理。
func (r *LotRepo) Insert(ctx context.Context, l *domain.MaterialLot) error {
	now := nowUTC()
	l.CreatedAt, l.UpdatedAt = now, now
	l.Version = 1
	if l.Status == "" {
		l.Status = domain.LotStatusRegistered
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO material_lots (lot_no, supplier_id, heat_no, grade, quantity, status,
			initial_result, retest_result, accepted_by, accepted_at, received_at, created_at, updated_at, version)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		l.LotNo, l.SupplierID, l.HeatNo, l.Grade, l.Quantity, string(l.Status),
		l.InitialResult, l.RetestResult, l.AcceptedBy, nullTimeToDB(l.AcceptedAt),
		timeToDB(l.ReceivedAt), timeToDB(l.CreatedAt), timeToDB(l.UpdatedAt), l.Version)
	if err != nil {
		if IsUniqueViolation(err) {
			return domain.Duplicate("material_lot", l.LotNo)
		}
		return fmt.Errorf("插入来料批次失败: %w", err)
	}
	l.ID, err = res.LastInsertId()
	return err
}

// GetByID 按主键查询批次，不存在返回 nil, nil。
func (r *LotRepo) GetByID(ctx context.Context, id int64) (*domain.MaterialLot, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+lotColumns+` FROM material_lots WHERE id = ?`, id)
	return scanLot(row)
}

// GetByLotNo 按批次号查询，不存在返回 nil, nil。
func (r *LotRepo) GetByLotNo(ctx context.Context, lotNo string) (*domain.MaterialLot, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+lotColumns+` FROM material_lots WHERE lot_no = ?`, lotNo)
	return scanLot(row)
}

func scanLot(row *sql.Row) (*domain.MaterialLot, error) {
	var l domain.MaterialLot
	var acceptedAt sql.NullString
	var receivedAt, created, updated string
	err := row.Scan(&l.ID, &l.LotNo, &l.SupplierID, &l.HeatNo, &l.Grade, &l.Quantity,
		&l.Status, &l.InitialResult, &l.RetestResult, &l.AcceptedBy, &acceptedAt,
		&receivedAt, &created, &updated, &l.Version)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if err := fillLotTimes(&l, acceptedAt, receivedAt, created, updated); err != nil {
		return nil, err
	}
	return &l, nil
}

func fillLotTimes(l *domain.MaterialLot, acceptedAt sql.NullString, receivedAt, created, updated string) error {
	t, err := nullTimeFromDB(acceptedAt)
	if err != nil {
		return err
	}
	l.AcceptedAt = t
	if l.ReceivedAt, err = dbToTime(receivedAt); err != nil {
		return err
	}
	if l.CreatedAt, err = dbToTime(created); err != nil {
		return err
	}
	if l.UpdatedAt, err = dbToTime(updated); err != nil {
		return err
	}
	return nil
}

// Transition 在一次 UPDATE 中完成状态流转与可选字段更新，并做乐观锁校验。
// 返回 false 表示版本冲突或记录不存在，由服务层区分处理。
func (r *LotRepo) Transition(ctx context.Context, id int64, expectedVersion int64,
	next domain.LotStatus, initialResult, retestResult *string,
	acceptedBy *string, acceptedAt *time.Time) (bool, error) {
	now := timeToDB(nowUTC())
	res, err := r.db.ExecContext(ctx,
		`UPDATE material_lots SET status = ?,
			initial_result = COALESCE(?, initial_result),
			retest_result  = COALESCE(?, retest_result),
			accepted_by    = COALESCE(?, accepted_by),
			accepted_at    = COALESCE(?, accepted_at),
			updated_at = ?, version = version + 1
		 WHERE id = ? AND version = ?`,
		string(next), initialResult, retestResult, acceptedBy, nullTimeToDB(acceptedAt),
		now, id, expectedVersion)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// List 分页查询批次，支持状态/供方/牌号/批次号前缀/到货时间区间过滤与稳定排序。
func (r *LotRepo) List(ctx context.Context, f domain.LotFilter, p domain.PageRequest) (domain.Page[domain.MaterialLot], error) {
	where := []string{"1=1"}
	args := []interface{}{}
	if f.Status != "" {
		where = append(where, `status = ?`)
		args = append(args, string(f.Status))
	}
	if f.SupplierID > 0 {
		where = append(where, `supplier_id = ?`)
		args = append(args, f.SupplierID)
	}
	if f.Grade != "" {
		where = append(where, `grade = ?`)
		args = append(args, f.Grade)
	}
	if f.LotNoPrefix != "" {
		where = append(where, `lot_no LIKE ? ESCAPE '\'`)
		args = append(args, escapeLike(f.LotNoPrefix)+"%")
	}
	if f.ReceivedAfter != nil {
		where = append(where, `received_at >= ?`)
		args = append(args, timeToDB(*f.ReceivedAfter))
	}
	if f.ReceivedBefore != nil {
		where = append(where, `received_at <= ?`)
		args = append(args, timeToDB(*f.ReceivedBefore))
	}
	cond := strings.Join(where, " AND ")
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM material_lots WHERE `+cond, args...).Scan(&total); err != nil {
		return domain.Page[domain.MaterialLot]{}, err
	}
	sortCol := map[string]string{
		"id": "id", "lot_no": "lot_no", "grade": "grade",
		"received_at": "received_at", "status": "status",
	}[p.Sort]
	query := fmt.Sprintf(
		`SELECT %s FROM material_lots WHERE %s ORDER BY %s %s, id ASC LIMIT ? OFFSET ?`,
		lotColumns, cond, sortCol, p.Order)
	rows, err := r.db.QueryContext(ctx, query, append(args, p.PageSize, p.Offset())...)
	if err != nil {
		return domain.Page[domain.MaterialLot]{}, err
	}
	defer rows.Close()
	items := []domain.MaterialLot{}
	for rows.Next() {
		var l domain.MaterialLot
		var acceptedAt sql.NullString
		var receivedAt, created, updated string
		if err := rows.Scan(&l.ID, &l.LotNo, &l.SupplierID, &l.HeatNo, &l.Grade, &l.Quantity,
			&l.Status, &l.InitialResult, &l.RetestResult, &l.AcceptedBy, &acceptedAt,
			&receivedAt, &created, &updated, &l.Version); err != nil {
			return domain.Page[domain.MaterialLot]{}, err
		}
		if err := fillLotTimes(&l, acceptedAt, receivedAt, created, updated); err != nil {
			return domain.Page[domain.MaterialLot]{}, err
		}
		items = append(items, l)
	}
	if err := rows.Err(); err != nil {
		return domain.Page[domain.MaterialLot]{}, err
	}
	return domain.NewPage(items, total, p), nil
}
