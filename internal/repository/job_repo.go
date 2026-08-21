package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"metalmics/internal/domain"
)

// JobRepo 是后台任务的持久化仓库。
type JobRepo struct {
	db DBTX
}

// NewJobRepo 构造后台任务仓库。
func NewJobRepo(db DBTX) *JobRepo {
	return &JobRepo{db: db}
}

const jobColumns = `id, type, payload, status, attempts, max_attempts, last_error, run_at, created_at, updated_at, version`

// Insert 插入后台任务。
func (r *JobRepo) Insert(ctx context.Context, j *domain.BackgroundJob) error {
	now := nowUTC()
	j.CreatedAt, j.UpdatedAt = now, now
	j.Version = 1
	if j.Status == "" {
		j.Status = domain.JobStatusPending
	}
	if j.RunAt.IsZero() {
		j.RunAt = now
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO background_jobs (type, payload, status, attempts, max_attempts, last_error, run_at, created_at, updated_at, version)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.Type, j.Payload, string(j.Status), j.Attempts, j.MaxAttempts, j.LastError,
		timeToDB(j.RunAt), timeToDB(j.CreatedAt), timeToDB(j.UpdatedAt), j.Version)
	if err != nil {
		return fmt.Errorf("插入后台任务失败: %w", err)
	}
	j.ID, err = res.LastInsertId()
	return err
}

// GetByID 按主键查询后台任务。
func (r *JobRepo) GetByID(ctx context.Context, id int64) (*domain.BackgroundJob, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM background_jobs WHERE id = ?`, id)
	return scanJob(row)
}

func scanJob(row *sql.Row) (*domain.BackgroundJob, error) {
	var j domain.BackgroundJob
	var runAt, created, updated string
	if err := row.Scan(&j.ID, &j.Type, &j.Payload, &j.Status, &j.Attempts, &j.MaxAttempts,
		&j.LastError, &runAt, &created, &updated, &j.Version); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	var err error
	if j.RunAt, err = dbToTime(runAt); err != nil {
		return nil, err
	}
	if j.CreatedAt, err = dbToTime(created); err != nil {
		return nil, err
	}
	if j.UpdatedAt, err = dbToTime(updated); err != nil {
		return nil, err
	}
	return &j, nil
}

// PickDue 原子地领取一个到期的 pending 任务并置为 running，
// 无任务可领时返回 nil, nil。领取在同一事务内完成以避免并发重复执行。
func (r *JobRepo) PickDue(ctx context.Context, now time.Time) (*domain.BackgroundJob, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+jobColumns+` FROM background_jobs
		 WHERE status = 'pending' AND run_at <= ? AND attempts < max_attempts
		 ORDER BY run_at DESC, id ASC LIMIT 1`, timeToDB(now))
	j, err := scanJob(row)
	if err != nil || j == nil {
		return nil, err
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE background_jobs SET status = 'running', attempts = attempts + 1,
			updated_at = ?, version = version + 1
		 WHERE id = ? AND status = 'pending' AND version = ?`,
		timeToDB(now), j.ID, j.Version)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil // 被其他调度者抢先领取
	}
	j.Status = domain.JobStatusRunning
	j.Attempts++
	return j, nil
}

// MarkDone 标记任务完成。
func (r *JobRepo) MarkDone(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE background_jobs SET status = 'done', last_error = '', updated_at = ?, version = version + 1 WHERE id = ?`,
		timeToDB(nowUTC()), id)
	return err
}

// MarkRetry 标记任务失败并安排下次重试；超过最大次数则进入 failed 终态。
func (r *JobRepo) MarkRetry(ctx context.Context, id int64, jobErr string, nextRunAt time.Time, exhausted bool) error {
	status := "pending"
	if exhausted {
		status = "failed"
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE background_jobs SET status = ?, last_error = ?, run_at = ?, updated_at = ?, version = version + 1 WHERE id = ?`,
		status, jobErr, timeToDB(nextRunAt), timeToDB(nowUTC()), id)
	return err
}

// RequeueRunning 将进程退出遗留的 running 任务重置为 pending（重启恢复）。
func (r *JobRepo) RequeueRunning(ctx context.Context) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE background_jobs SET status = 'pending', updated_at = ?, version = version + 1 WHERE status = 'running'`,
		timeToDB(nowUTC()))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RetryFailed 将 failed 任务重新置为 pending 并清零重试计数（人工重试）。
func (r *JobRepo) RetryFailed(ctx context.Context, id int64) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE background_jobs SET status = 'pending', attempts = 0, last_error = '',
			run_at = ?, updated_at = ?, version = version + 1
		 WHERE id = ? AND status = 'failed'`,
		timeToDB(nowUTC()), timeToDB(nowUTC()), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// List 分页查询后台任务，支持状态与类型过滤、稳定排序。
func (r *JobRepo) List(ctx context.Context, f domain.JobFilter, p domain.PageRequest) (domain.Page[domain.BackgroundJob], error) {
	where := []string{"1=1"}
	args := []interface{}{}
	if f.Status != "" {
		where = append(where, `status = ?`)
		args = append(args, string(f.Status))
	}
	if f.Type != "" {
		where = append(where, `type = ?`)
		args = append(args, f.Type)
	}
	cond := strings.Join(where, " AND ")
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM background_jobs WHERE `+cond, args...).Scan(&total); err != nil {
		return domain.Page[domain.BackgroundJob]{}, err
	}
	sortCol := map[string]string{"id": "id", "run_at": "run_at", "created_at": "created_at"}[p.Sort]
	query := fmt.Sprintf(
		`SELECT %s FROM background_jobs WHERE %s ORDER BY %s %s, id ASC LIMIT ? OFFSET ?`,
		jobColumns, cond, sortCol, p.Order)
	rows, err := r.db.QueryContext(ctx, query, append(args, p.PageSize, p.Offset())...)
	if err != nil {
		return domain.Page[domain.BackgroundJob]{}, err
	}
	defer rows.Close()
	items := []domain.BackgroundJob{}
	for rows.Next() {
		var j domain.BackgroundJob
		var runAt, created, updated string
		if err := rows.Scan(&j.ID, &j.Type, &j.Payload, &j.Status, &j.Attempts, &j.MaxAttempts,
			&j.LastError, &runAt, &created, &updated, &j.Version); err != nil {
			return domain.Page[domain.BackgroundJob]{}, err
		}
		if j.RunAt, err = dbToTime(runAt); err != nil {
			return domain.Page[domain.BackgroundJob]{}, err
		}
		if j.CreatedAt, err = dbToTime(created); err != nil {
			return domain.Page[domain.BackgroundJob]{}, err
		}
		if j.UpdatedAt, err = dbToTime(updated); err != nil {
			return domain.Page[domain.BackgroundJob]{}, err
		}
		items = append(items, j)
	}
	if err := rows.Err(); err != nil {
		return domain.Page[domain.BackgroundJob]{}, err
	}
	return domain.NewPage(items, total, p), nil
}
