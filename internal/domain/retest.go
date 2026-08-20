package domain

import "time"

// RetestStatus 是复验任务状态。
type RetestStatus string

const (
	RetestStatusOpen      RetestStatus = "open"      // 已申请，待批准
	RetestStatusApproved  RetestStatus = "approved"  // 已批准，可执行复验
	RetestStatusConcluded RetestStatus = "concluded" // 已出复验结论
	RetestStatusRejected  RetestStatus = "rejected"  // 申请被驳回
)

// RetestTask 是异议复验任务。
// 复验必须基于留样（原样），结论覆盖初检时需共同决定。
// 同一批次同一时刻仅允许一个未关闭的复验任务（open/approved 唯一）。
type RetestTask struct {
	ID          int64        `json:"id"`
	LotID       int64        `json:"lot_id"`
	SampleID    int64        `json:"sample_id"` // 复验所用留样
	Reason      string       `json:"reason"`
	Status      RetestStatus `json:"status"`
	RequestedBy string       `json:"requested_by"`
	ApprovedBy  string       `json:"approved_by,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	Version     int64        `json:"version"`
}

// Validate 校验复验任务字段。
func (t *RetestTask) Validate() error {
	if t.LotID <= 0 {
		return Validation("lot_id", "必须关联来料批次")
	}
	if t.SampleID <= 0 {
		return Validation("sample_id", "必须指定复验留样")
	}
	if t.Reason == "" {
		return Validation("reason", "异议原因不能为空")
	}
	if t.RequestedBy == "" {
		return Validation("requested_by", "申请人不能为空")
	}
	return nil
}
