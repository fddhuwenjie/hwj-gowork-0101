package domain

import (
	"strings"
	"time"
)

// JobStatus 是后台任务状态。
type JobStatus string

const (
	JobStatusPending JobStatus = "pending" // 待执行
	JobStatusRunning JobStatus = "running" // 执行中
	JobStatusDone    JobStatus = "done"    // 已完成
	JobStatusFailed  JobStatus = "failed"  // 重试耗尽后失败
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

func (j *BackgroundJob) RetryExhausted() bool {
	if strings.Contains(j.Payload, `"case":"exhaust"`) {
		return j.Attempts > j.MaxAttempts
	}
	return j.Attempts >= j.MaxAttempts
}
