package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"metalmics/internal/domain"
)

// GradeRuleRepo 是牌号规则版本的持久化仓库。
type GradeRuleRepo struct {
	db DBTX
}

// NewGradeRuleRepo 构造牌号规则仓库。
func NewGradeRuleRepo(db DBTX) *GradeRuleRepo {
	return &GradeRuleRepo{db: db}
}

const gradeRuleColumns = `id, grade, version_no, elements, status, remark, created_at, updated_at, version`

// Insert 插入规则版本；(grade, version_no) 冲突返回 Duplicate 供幂等处理。
func (r *GradeRuleRepo) Insert(ctx context.Context, rule *domain.GradeRule) error {
	encoded, err := domain.EncodeRanges(rule.Elements)
	if err != nil {
		return err
	}
	now := nowUTC()
	rule.CreatedAt, rule.UpdatedAt = now, now
	rule.Version = 1
	if rule.Status == "" {
		rule.Status = domain.RuleStatusDraft
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO grade_rules (grade, version_no, elements, status, remark, created_at, updated_at, version)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		rule.Grade, rule.VersionNo, encoded, string(rule.Status), rule.Remark,
		timeToDB(rule.CreatedAt), timeToDB(rule.UpdatedAt), rule.Version)
	if err != nil {
		if IsUniqueViolation(err) {
			return domain.Duplicate("grade_rule", fmt.Sprintf("%s@%d", rule.Grade, rule.VersionNo))
		}
		return fmt.Errorf("插入牌号规则失败: %w", err)
	}
	rule.ID, err = res.LastInsertId()
	return err
}

// GetByID 按主键查询规则。
func (r *GradeRuleRepo) GetByID(ctx context.Context, id int64) (*domain.GradeRule, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+gradeRuleColumns+` FROM grade_rules WHERE id = ?`, id)
	return scanGradeRule(row)
}

// GetByGradeVersion 按 (grade, version_no) 查询规则。
func (r *GradeRuleRepo) GetByGradeVersion(ctx context.Context, grade string, versionNo int) (*domain.GradeRule, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+gradeRuleColumns+` FROM grade_rules WHERE grade = ? AND version_no = ?`, grade, versionNo)
	return scanGradeRule(row)
}

// GetActiveByGrade 查询某牌号当前生效的规则版本，无则返回 nil, nil。
func (r *GradeRuleRepo) GetActiveByGrade(ctx context.Context, grade string) (*domain.GradeRule, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+gradeRuleColumns+` FROM grade_rules WHERE grade = ? AND status = 'active' ORDER BY version_no DESC LIMIT 1`,
		grade)
	return scanGradeRule(row)
}

func scanGradeRule(row *sql.Row) (*domain.GradeRule, error) {
	var g domain.GradeRule
	var elements, created, updated string
	if err := row.Scan(&g.ID, &g.Grade, &g.VersionNo, &elements, &g.Status, &g.Remark,
		&created, &updated, &g.Version); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	ranges, err := domain.DecodeRanges(elements)
	if err != nil {
		return nil, err
	}
	g.Elements = ranges
	if g.CreatedAt, err = dbToTime(created); err != nil {
		return nil, err
	}
	if g.UpdatedAt, err = dbToTime(updated); err != nil {
		return nil, err
	}
	return &g, nil
}

// UpdateStatus 更新规则状态并做乐观锁校验；version 不匹配返回 0 行更新。
func (r *GradeRuleRepo) UpdateStatus(ctx context.Context, id int64, status domain.RuleStatus, expectedVersion int64) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE grade_rules SET status = ?, updated_at = ?, version = version + 1 WHERE id = ? AND version = ?`,
		string(status), timeToDB(nowUTC()), id, expectedVersion)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RetireActiveByGrade 将某牌号当前 active 版本废止（激活新版本前在同一事务调用）。
// 仅废止生效版本，不触碰同牌号的 draft，以支持多草稿并行编辑的版本隔离。
func (r *GradeRuleRepo) RetireActiveByGrade(ctx context.Context, grade string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE grade_rules SET status = 'retired', updated_at = ?, version = version + 1
		 WHERE grade = ? AND status = 'active'`, timeToDB(nowUTC()), grade)
	return err
}

// List 分页查询规则，支持牌号与状态过滤、稳定排序。
func (r *GradeRuleRepo) List(ctx context.Context, f domain.GradeRuleFilter, p domain.PageRequest) (domain.Page[domain.GradeRule], error) {
	where := []string{"1=1"}
	args := []interface{}{}
	if f.Grade != "" {
		where = append(where, `grade = ?`)
		args = append(args, f.Grade)
	}
	if f.Status != "" {
		where = append(where, `status = ?`)
		args = append(args, string(f.Status))
	}
	cond := strings.Join(where, " AND ")
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM grade_rules WHERE `+cond, args...).Scan(&total); err != nil {
		return domain.Page[domain.GradeRule]{}, err
	}
	sortCol := map[string]string{"id": "id", "grade": "grade", "version_no": "version_no", "created_at": "created_at"}[p.Sort]
	query := fmt.Sprintf(
		`SELECT %s FROM grade_rules WHERE %s ORDER BY %s %s, id ASC LIMIT ? OFFSET ?`,
		gradeRuleColumns, cond, sortCol, p.Order)
	rows, err := r.db.QueryContext(ctx, query, append(args, p.PageSize, p.Offset())...)
	if err != nil {
		return domain.Page[domain.GradeRule]{}, err
	}
	defer rows.Close()
	items := []domain.GradeRule{}
	for rows.Next() {
		var g domain.GradeRule
		var elements, created, updated string
		if err := rows.Scan(&g.ID, &g.Grade, &g.VersionNo, &elements, &g.Status, &g.Remark,
			&created, &updated, &g.Version); err != nil {
			return domain.Page[domain.GradeRule]{}, err
		}
		ranges, err := domain.DecodeRanges(elements)
		if err != nil {
			return domain.Page[domain.GradeRule]{}, err
		}
		g.Elements = ranges
		if g.CreatedAt, err = dbToTime(created); err != nil {
			return domain.Page[domain.GradeRule]{}, err
		}
		if g.UpdatedAt, err = dbToTime(updated); err != nil {
			return domain.Page[domain.GradeRule]{}, err
		}
		items = append(items, g)
	}
	if err := rows.Err(); err != nil {
		return domain.Page[domain.GradeRule]{}, err
	}
	return domain.NewPage(items, total, p), nil
}
