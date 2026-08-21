package repository

import (
	"context"
	"database/sql"
	"fmt"

	"metalmics/internal/domain"
)

// SamplingPlanRepo 是取样计划的持久化仓库。
type SamplingPlanRepo struct {
	db DBTX
}

// NewSamplingPlanRepo 构造取样计划仓库。
func NewSamplingPlanRepo(db DBTX) *SamplingPlanRepo {
	return &SamplingPlanRepo{db: db}
}

const planColumns = `id, plan_no, lot_id, required_count, retain_location, status, created_by, created_at, updated_at, version`

// Insert 插入取样计划；plan_no 或 lot_id 冲突返回 Duplicate 供幂等处理。
func (r *SamplingPlanRepo) Insert(ctx context.Context, p *domain.SamplingPlan) error {
	now := nowUTC()
	p.CreatedAt, p.UpdatedAt = now, now
	p.Version = 1
	if p.Status == "" {
		p.Status = domain.PlanStatusActive
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO sampling_plans (plan_no, lot_id, required_count, retain_location, status, created_by, created_at, updated_at, version)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.PlanNo, p.LotID, p.RequiredCount, p.RetainLocation, string(p.Status), p.CreatedBy,
		timeToDB(p.CreatedAt), timeToDB(p.UpdatedAt), p.Version)
	if err != nil {
		if IsUniqueViolation(err) {
			return domain.Duplicate("sampling_plan", p.PlanNo)
		}
		return fmt.Errorf("插入取样计划失败: %w", err)
	}
	p.ID, err = res.LastInsertId()
	return err
}

// GetByID 按主键查询取样计划。
func (r *SamplingPlanRepo) GetByID(ctx context.Context, id int64) (*domain.SamplingPlan, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+planColumns+` FROM sampling_plans WHERE id = ?`, id)
	return scanPlan(row)
}

// GetByPlanNo 按计划编号查询。
func (r *SamplingPlanRepo) GetByPlanNo(ctx context.Context, planNo string) (*domain.SamplingPlan, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+planColumns+` FROM sampling_plans WHERE plan_no = ?`, planNo)
	return scanPlan(row)
}

// GetByLot 查询批次的取样计划（每批次最多一份）。
func (r *SamplingPlanRepo) GetByLot(ctx context.Context, lotID int64) (*domain.SamplingPlan, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+planColumns+` FROM sampling_plans WHERE lot_id = ? ORDER BY id DESC`, lotID)
	return scanPlan(row)
}

func scanPlan(row *sql.Row) (*domain.SamplingPlan, error) {
	var p domain.SamplingPlan
	var created, updated string
	if err := row.Scan(&p.ID, &p.PlanNo, &p.LotID, &p.RequiredCount, &p.RetainLocation,
		&p.Status, &p.CreatedBy, &created, &updated, &p.Version); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	var err error
	if p.CreatedAt, err = dbToTime(created); err != nil {
		return nil, err
	}
	if p.UpdatedAt, err = dbToTime(updated); err != nil {
		return nil, err
	}
	return &p, nil
}

// UpdateStatus 更新计划状态，带乐观锁校验。
func (r *SamplingPlanRepo) UpdateStatus(ctx context.Context, id int64, expectedVersion int64, status domain.PlanStatus) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE sampling_plans SET status = ?, updated_at = ?, version = version + 1 WHERE id = ? AND version = ?`,
		string(status), timeToDB(nowUTC()), id, expectedVersion)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
