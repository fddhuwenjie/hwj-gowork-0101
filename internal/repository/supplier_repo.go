package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"metalmics/internal/domain"
)

// SupplierRepo 是供方的持久化仓库。
type SupplierRepo struct {
	db DBTX
}

// NewSupplierRepo 构造供方仓库，db 可以是连接池或事务。
func NewSupplierRepo(db DBTX) *SupplierRepo {
	return &SupplierRepo{db: db}
}

// Insert 插入供方；编码冲突时返回领域 Duplicate 错误供幂等处理。
func (r *SupplierRepo) Insert(ctx context.Context, s *domain.Supplier) error {
	s.CreatedAt = nowUTC()
	s.Version = 1
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO suppliers (code, name, contact, created_at, version) VALUES (?, ?, ?, ?, ?)`,
		s.Code, s.Name, s.Contact, timeToDB(s.CreatedAt), s.Version)
	if err != nil {
		if IsUniqueViolation(err) {
			return domain.Duplicate("supplier", s.Code)
		}
		return fmt.Errorf("插入供方失败: %w", err)
	}
	s.ID, err = res.LastInsertId()
	return err
}

// GetByID 按主键查询供方，不存在返回 nil, nil。
func (r *SupplierRepo) GetByID(ctx context.Context, id int64) (*domain.Supplier, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, code, name, contact, created_at, version FROM suppliers WHERE id = ?`, id)
	return scanSupplier(row)
}

// GetByCode 按编码查询供方，不存在返回 nil, nil。
func (r *SupplierRepo) GetByCode(ctx context.Context, code string) (*domain.Supplier, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, code, name, contact, created_at, version FROM suppliers WHERE code = ?`, code)
	return scanSupplier(row)
}

func scanSupplier(row *sql.Row) (*domain.Supplier, error) {
	var s domain.Supplier
	var created string
	if err := row.Scan(&s.ID, &s.Code, &s.Name, &s.Contact, &created, &s.Version); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	t, err := dbToTime(created)
	if err != nil {
		return nil, err
	}
	s.CreatedAt = t
	return &s, nil
}

// List 分页查询供方，支持编码前缀与名称模糊过滤、稳定排序。
func (r *SupplierRepo) List(ctx context.Context, f domain.SupplierFilter, p domain.PageRequest) (domain.Page[domain.Supplier], error) {
	where := []string{"1=1"}
	args := []interface{}{}
	if f.CodePrefix != "" {
		where = append(where, `code LIKE ? ESCAPE '\'`)
		args = append(args, escapeLike(f.CodePrefix)+"%")
	}
	if f.NameLike != "" {
		where = append(where, `name LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(f.NameLike)+"%")
	}
	cond := strings.Join(where, " AND ")
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM suppliers WHERE `+cond, args...).Scan(&total); err != nil {
		return domain.Page[domain.Supplier]{}, err
	}
	sortCol := map[string]string{"id": "id", "code": "code", "created_at": "created_at"}[p.Sort]
	query := fmt.Sprintf(
		`SELECT id, code, name, contact, created_at, version FROM suppliers WHERE %s ORDER BY %s %s, id ASC LIMIT ? OFFSET ?`,
		cond, sortCol, p.Order)
	rows, err := r.db.QueryContext(ctx, query, append(args, p.PageSize, p.Offset())...)
	if err != nil {
		return domain.Page[domain.Supplier]{}, err
	}
	defer rows.Close()
	items := []domain.Supplier{}
	for rows.Next() {
		var s domain.Supplier
		var created string
		if err := rows.Scan(&s.ID, &s.Code, &s.Name, &s.Contact, &created, &s.Version); err != nil {
			return domain.Page[domain.Supplier]{}, err
		}
		t, err := dbToTime(created)
		if err != nil {
			return domain.Page[domain.Supplier]{}, err
		}
		s.CreatedAt = t
		items = append(items, s)
	}
	if err := rows.Err(); err != nil {
		return domain.Page[domain.Supplier]{}, err
	}
	return domain.NewPage(items, total, p), nil
}
