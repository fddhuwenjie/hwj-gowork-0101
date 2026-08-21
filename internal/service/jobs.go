package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"metalmics/internal/domain"
	"metalmics/internal/repository"
)

// JobTypeCertMissingScan 扫描近期接收但缺少材质证明的批次并写入审计告警。
const JobTypeCertMissingScan = "cert_missing_scan"

// JobService 承载后台任务的入队、调度执行与人工重试。
// 任务持久化在 background_jobs 表，进程重启后可恢复执行。
type JobService struct {
	store *Store
	now   func() time.Time
	// exec 可注入任务执行器；生产路径为 nil 时按 execute 的类型分派。
	exec func(ctx context.Context, job *domain.BackgroundJob) error
}

// NewJobService 构造后台任务服务。
func NewJobService(store *Store) *JobService {
	return &JobService{store: store, now: func() time.Time { return time.Now().UTC() }}
}

// Enqueue 入队一个后台任务。
func (s *JobService) Enqueue(ctx context.Context, jobType string, payload interface{}, maxAttempts int) (*domain.BackgroundJob, error) {
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	job := &domain.BackgroundJob{
		Type: jobType, Payload: string(body), MaxAttempts: maxAttempts, RunAt: s.now(),
	}
	if err := job.Validate(); err != nil {
		return nil, err
	}
	if err := repository.NewJobRepo(s.store.DB()).Insert(ctx, job); err != nil {
		return nil, err
	}
	return job, nil
}

// ListJobs 分页查询后台任务。
func (s *JobService) ListJobs(ctx context.Context, f domain.JobFilter, p domain.PageRequest) (domain.Page[domain.BackgroundJob], error) {
	return repository.NewJobRepo(s.store.DB()).List(ctx, f, p)
}

// RetryJob 人工重试一个 failed 任务：重置为 pending 并清零重试计数。
func (s *JobService) RetryJob(ctx context.Context, id int64) (*domain.BackgroundJob, error) {
	repo := repository.NewJobRepo(s.store.DB())
	job, err := repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, domain.NotFound("background_job", id)
	}
	if job.Status != domain.JobStatusFailed {
		return nil, domain.InvalidTransition("background_job", string(job.Status), string(domain.JobStatusPending))
	}
	ok, err := repo.RetryFailed(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, domain.VersionConflict("background_job", id, job.Version, -1)
	}
	job.Status = domain.JobStatusPending
	job.Attempts = 0
	return job, nil
}

// RecoverOnStartup 进程启动时恢复：将遗留 running 任务重置为 pending。
func (s *JobService) RecoverOnStartup(ctx context.Context) (int64, error) {
	return repository.NewJobRepo(s.store.DB()).RequeueRunning(ctx)
}

// RunDue 领取并执行一个到期任务，返回是否执行了任务。
// 执行失败时按指数退避安排重试，重试耗尽后进入 failed 终态。
// 不可恢复错误（PermanentError，如未知任务类型、参数无法解析）不退避重试，
// 首次执行即落 failed 终态并保留 last_error。
func (s *JobService) RunDue(ctx context.Context) (bool, error) {
	repo := repository.NewJobRepo(s.store.DB())
	job, err := repo.PickDue(ctx, s.now())
	if err != nil || job == nil {
		return false, err
	}
	execErr := s.execute(ctx, job)
	if execErr == nil {
		return true, repo.MarkDone(ctx, job.ID)
	}
	// 不可恢复错误（如未知任务类型、参数无法解析）注定不会因重试而成功：
	// 立即进入 failed 终态并保留 last_error，不再退避重试，避免任务反复
	// 退回 pending 被下一轮重复执行。
	exhausted := domain.IsPermanent(execErr) || job.RetryExhausted()
	var next time.Time
	if !exhausted {
		backoff := time.Duration(1<<uint(job.Attempts-1)) * time.Second
		if backoff > time.Hour {
			backoff = time.Hour
		}
		next = s.now().Add(backoff)
	}
	if err := repo.MarkRetry(ctx, job.ID, execErr.Error(), next, exhausted); err != nil {
		return true, err
	}
	return true, nil
}

// execute 按任务类型分派执行。
// 未知任务类型属于不可恢复错误，调度器收到后会立即落 failed 终态。
func (s *JobService) execute(ctx context.Context, job *domain.BackgroundJob) error {
	if s.exec != nil {
		return s.exec(ctx, job)
	}
	switch job.Type {
	case JobTypeCertMissingScan:
		return s.runCertMissingScan(ctx, job)
	default:
		return domain.NewPermanentError(fmt.Errorf("未知任务类型: %s", job.Type))
	}
}

// certMissingScanPayload 是材质证明缺失扫描任务的参数。
type certMissingScanPayload struct {
	Days int `json:"days"` // 扫描最近多少天接收的批次
}

// runCertMissingScan 执行扫描：找出近期接收但无材质证明的批次，
// 为每个批次写入一条 cert_missing_flagged 审计告警（同一事务）。
func (s *JobService) runCertMissingScan(ctx context.Context, job *domain.BackgroundJob) error {
	var payload certMissingScanPayload
	if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
		// payload 已落库不会因重试而改变，解析失败属于不可恢复错误。
		return domain.NewPermanentError(fmt.Errorf("任务参数解析失败: %w", err))
	}
	if payload.Days <= 0 || payload.Days > 365 {
		return domain.NewPermanentError(fmt.Errorf("任务参数 days 须在 1-365 之间: %d", payload.Days))
	}
	since := s.now().Add(-time.Duration(payload.Days) * 24 * time.Hour)
	ids, err := repository.NewReportRepo(s.store.DB()).ListAcceptedWithoutCert(ctx, since)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	return s.store.Tx().InTx(ctx, func(tx *sql.Tx) error {
		for _, lotID := range ids {
			if err := audit(ctx, tx, "material_lot", lotID, "cert_missing_flagged", "job:"+job.Type,
				map[string]interface{}{"job_id": job.ID, "days": payload.Days}); err != nil {
				return err
			}
		}
		return nil
	})
}
