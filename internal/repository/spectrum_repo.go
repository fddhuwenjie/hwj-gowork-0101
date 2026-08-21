package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"metalmics/internal/domain"
)

// SpectrumRepo 是光谱分析报告的持久化仓库。
type SpectrumRepo struct {
	db DBTX
}

// NewSpectrumRepo 构造光谱报告仓库。
func NewSpectrumRepo(db DBTX) *SpectrumRepo {
	return &SpectrumRepo{db: db}
}

const spectrumColumns = `id, report_no, sample_id, rule_id, readings, violations, conclusion, analyzer, created_at, version`

// Insert 插入光谱报告；report_no 或 sample_id 冲突返回 Duplicate 供幂等处理。
func (r *SpectrumRepo) Insert(ctx context.Context, rep *domain.SpectrumReport) error {
	readings, err := domain.EncodeReadings(rep.Readings)
	if err != nil {
		return err
	}
	violations, err := json.Marshal(rep.Violations)
	if err != nil {
		return err
	}
	rep.CreatedAt = nowUTC()
	rep.Version = 1
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO spectrum_reports (report_no, sample_id, rule_id, readings, violations, conclusion, analyzer, created_at, version)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rep.ReportNo, rep.SampleID, rep.RuleID, readings, string(violations),
		string(rep.Conclusion), rep.Analyzer, timeToDB(rep.CreatedAt), rep.Version)
	if err != nil {
		if IsUniqueViolation(err) {
			return domain.Duplicate("spectrum_report", rep.ReportNo)
		}
		return fmt.Errorf("插入光谱报告失败: %w", err)
	}
	rep.ID, err = res.LastInsertId()
	return err
}

// GetByID 按主键查询光谱报告。
func (r *SpectrumRepo) GetByID(ctx context.Context, id int64) (*domain.SpectrumReport, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+spectrumColumns+` FROM spectrum_reports WHERE id = ?`, id)
	return scanSpectrum(row)
}

// GetByReportNo 按报告编号查询。
func (r *SpectrumRepo) GetByReportNo(ctx context.Context, reportNo string) (*domain.SpectrumReport, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+spectrumColumns+` FROM spectrum_reports WHERE report_no = ?`, reportNo)
	return scanSpectrum(row)
}

// ListBySample 查询样本的全部光谱报告（初检一份 + 复验可多份），按 id 升序。
func (r *SpectrumRepo) ListBySample(ctx context.Context, sampleID int64) ([]domain.SpectrumReport, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+spectrumColumns+` FROM spectrum_reports WHERE sample_id = ? ORDER BY id ASC`, sampleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.SpectrumReport{}
	for rows.Next() {
		var rep domain.SpectrumReport
		var readings, violations, created string
		if err := rows.Scan(&rep.ID, &rep.ReportNo, &rep.SampleID, &rep.RuleID, &readings, &violations,
			&rep.Conclusion, &rep.Analyzer, &created, &rep.Version); err != nil {
			return nil, err
		}
		if err := fillSpectrumFields(&rep, readings, violations, created); err != nil {
			return nil, err
		}
		items = append(items, rep)
	}
	return items, rows.Err()
}

func scanSpectrum(row *sql.Row) (*domain.SpectrumReport, error) {
	var rep domain.SpectrumReport
	var readings, violations, created string
	if err := row.Scan(&rep.ID, &rep.ReportNo, &rep.SampleID, &rep.RuleID, &readings, &violations,
		&rep.Conclusion, &rep.Analyzer, &created, &rep.Version); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if err := fillSpectrumFields(&rep, readings, violations, created); err != nil {
		return nil, err
	}
	return &rep, nil
}

func fillSpectrumFields(rep *domain.SpectrumReport, readings, violations, created string) error {
	rd, err := domain.DecodeReadings(readings)
	if err != nil {
		return err
	}
	rep.Readings = rd
	var v []domain.RangeViolation
	if err := json.Unmarshal([]byte(violations), &v); err != nil {
		return err
	}
	rep.Violations = v
	t, err := dbToTime(created)
	if err != nil {
		return err
	}
	rep.CreatedAt = t
	return nil
}

// ListByLot 查询批次全部样本的光谱报告（经取样计划关联），按报告 id 升序稳定排列。
func (r *SpectrumRepo) ListByLot(ctx context.Context, lotID int64) ([]domain.SpectrumReport, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT sr.id, sr.report_no, sr.sample_id, sr.rule_id, sr.readings, sr.violations,
		        sr.conclusion, sr.analyzer, sr.created_at, sr.version
		 FROM spectrum_reports sr
		 JOIN samples s ON s.id = sr.sample_id
		 JOIN sampling_plans sp ON sp.id = s.plan_id
		 WHERE sp.lot_id = ?
		 ORDER BY sr.id DESC`, lotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.SpectrumReport{}
	for rows.Next() {
		var rep domain.SpectrumReport
		var readings, violations, created string
		if err := rows.Scan(&rep.ID, &rep.ReportNo, &rep.SampleID, &rep.RuleID, &readings, &violations,
			&rep.Conclusion, &rep.Analyzer, &created, &rep.Version); err != nil {
			return nil, err
		}
		if err := fillSpectrumFields(&rep, readings, violations, created); err != nil {
			return nil, err
		}
		items = append(items, rep)
	}
	return items, rows.Err()
}

// ListBySampleKind 查询批次下指定样本类型的光谱报告，供判定流程使用。
func (r *SpectrumRepo) ListBySampleKind(ctx context.Context, lotID int64, kind domain.SampleKind) ([]domain.SpectrumReport, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT sr.id, sr.report_no, sr.sample_id, sr.rule_id, sr.readings, sr.violations,
		        sr.conclusion, sr.analyzer, sr.created_at, sr.version
		 FROM spectrum_reports sr
		 JOIN samples s ON s.id = sr.sample_id
		 JOIN sampling_plans sp ON sp.id = s.plan_id
		 WHERE sp.lot_id = ? AND s.kind = ?
		 ORDER BY sr.id ASC`, lotID, string(kind))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.SpectrumReport{}
	for rows.Next() {
		var rep domain.SpectrumReport
		var readings, violations, created string
		if err := rows.Scan(&rep.ID, &rep.ReportNo, &rep.SampleID, &rep.RuleID, &readings, &violations,
			&rep.Conclusion, &rep.Analyzer, &created, &rep.Version); err != nil {
			return nil, err
		}
		if err := fillSpectrumFields(&rep, readings, violations, created); err != nil {
			return nil, err
		}
		items = append(items, rep)
	}
	return items, rows.Err()
}
