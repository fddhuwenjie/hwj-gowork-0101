package repository

import (
	"context"
	"database/sql"
	"fmt"

	"metalmics/internal/domain"
)

// SampleRepo 是取样样本的持久化仓库。
type SampleRepo struct {
	db DBTX
}

// NewSampleRepo 构造样本仓库。
func NewSampleRepo(db DBTX) *SampleRepo {
	return &SampleRepo{db: db}
}

const sampleColumns = `id, plan_id, sample_no, kind, retained, status, created_at, version`

// Insert 插入样本；(plan_id, sample_no) 冲突返回 Duplicate 供幂等处理。
func (r *SampleRepo) Insert(ctx context.Context, s *domain.Sample) error {
	s.CreatedAt = nowUTC()
	s.Version = 1
	if s.Status == "" {
		s.Status = domain.SampleStatusCreated
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO samples (plan_id, sample_no, kind, retained, status, created_at, version)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.PlanID, s.SampleNo, string(s.Kind), boolToInt(s.Retained), string(s.Status),
		timeToDB(s.CreatedAt), s.Version)
	if err != nil {
		if IsUniqueViolation(err) {
			return domain.Duplicate("sample", fmt.Sprintf("%d/%s", s.PlanID, s.SampleNo))
		}
		return fmt.Errorf("插入样本失败: %w", err)
	}
	s.ID, err = res.LastInsertId()
	return err
}

// GetByID 按主键查询样本。
func (r *SampleRepo) GetByID(ctx context.Context, id int64) (*domain.Sample, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+sampleColumns+` FROM samples WHERE id = ?`, id)
	return scanSample(row)
}

// GetByPlanAndNo 按计划与样本编号查询。
func (r *SampleRepo) GetByPlanAndNo(ctx context.Context, planID int64, sampleNo string) (*domain.Sample, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+sampleColumns+` FROM samples WHERE plan_id = ? AND sample_no = ?`, planID, sampleNo)
	return scanSample(row)
}

func scanSample(row *sql.Row) (*domain.Sample, error) {
	var s domain.Sample
	var retained int
	var created string
	if err := row.Scan(&s.ID, &s.PlanID, &s.SampleNo, &s.Kind, &retained, &s.Status, &created, &s.Version); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	s.Retained = retained == 1
	t, err := dbToTime(created)
	if err != nil {
		return nil, err
	}
	s.CreatedAt = t
	return &s, nil
}

// ListByPlan 查询计划下全部样本，按 id 升序稳定排列。
func (r *SampleRepo) ListByPlan(ctx context.Context, planID int64) ([]domain.Sample, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+sampleColumns+` FROM samples WHERE plan_id = ? ORDER BY id ASC`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.Sample{}
	for rows.Next() {
		var s domain.Sample
		var retained int
		var created string
		if err := rows.Scan(&s.ID, &s.PlanID, &s.SampleNo, &s.Kind, &retained, &s.Status, &created, &s.Version); err != nil {
			return nil, err
		}
		s.Retained = retained == 1
		t, err := dbToTime(created)
		if err != nil {
			return nil, err
		}
		s.CreatedAt = t
		items = append(items, s)
	}
	return items, rows.Err()
}

// CountByPlanAndKind 统计计划下某类样本数量（用于 R06 规则校验）。
func (r *SampleRepo) CountByPlanAndKind(ctx context.Context, planID int64, kind domain.SampleKind) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM samples WHERE plan_id = ? AND kind = ?`, planID, string(kind)).Scan(&n)
	return n, err
}

// UpdateStatus 更新样本状态，带乐观锁校验。
func (r *SampleRepo) UpdateStatus(ctx context.Context, id int64, expectedVersion int64, status domain.SampleStatus) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE samples SET status = ?, version = version + 1 WHERE id = ? AND version = ?`,
		string(status), id, expectedVersion)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
