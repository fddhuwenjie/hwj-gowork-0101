package domain

import (
	"errors"
	"time"
)

// JobStatus 是后台任务状态。
type JobStatus string

const (
	JobStatusPending JobStatus = "pending" // 待执行
	JobStatusRunning JobStatus = "running" // 执行中
	JobStatusDone    JobStatus = "done"    // 已完成
	JobStatusFailed  JobStatus = "failed"  // 重试耗尽或不可恢复错误后失败终态
)

// BackgroundJob 是可持久化后台任务。
// 任务落库后才被调度执行，进程重启后由调度器恢复 pending/running 任务，
// 执行失败按指数退避重试，超过 MaxAttempts 后进入 failed 终态。
type BackgroundJob struct {
	ID          int64      `json:"id"`
	Type        string     `json:"type"`    // 任务类型，如 cert_missing_scan
	Payload     string     `json:"payload"` // JSON 参数
	Status      JobStatus  `json:"status"`
	Attempts    int        `json:"attempts"`
	MaxAttempts int        `json:"max_attempts"`
	LastError   string     `json:"last_error,omitempty"`
	RunAt       time.Time  `json:"run_at"` // 下次可执行时间
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	Version     int64      `json:"version"`
}

// Validate 校验后台任务字段。
func (j *BackgroundJob) Validate() error {
	if j.Type == "" {
		return Validation("type", "任务类型不能为空")
	}
	if j.MaxAttempts <= 0 {
		return Validation("max_attempts", "最大重试次数必须为正数")
	}
	return nil
}

// PermanentError 标记不可恢复的任务错误：调度器在收到此类错误时
// 立即进入 failed 终态并保留 last_error，不再退避重试。
// 典型情形：未知任务类型、参数无法解析等任何重试也注定失败的情况。
type PermanentError struct {
	err error
}

// NewPermanentError 包装一个错误为不可恢复错误。
func NewPermanentError(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{err: err}
}

// Error 实现 error 接口。
func (e *PermanentError) Error() string { return e.err.Error() }

// Unwrap 暴露被包装错误，支持 errors.Is/errors.As 沿调用链判断。
func (e *PermanentError) Unwrap() error { return e.err }

// IsPermanent 判断错误是否为不可恢复错误。
func IsPermanent(err error) bool {
	var pe *PermanentError
	return errors.As(err, &pe)
}

// RetryExhausted 判断重试是否已耗尽：达到配置的最大尝试次数即应进入
// failed 终态。无论任务以何种方式失败，attempts == max_attempts 时
// 都必须落终态并保留 last_error，避免任务滞留 pending 永远无法被
// 调度（PickDue 仅领取 attempts < max_attempts 的任务）。
func (j *BackgroundJob) RetryExhausted() bool {
	return j.Attempts >= j.MaxAttempts
}
