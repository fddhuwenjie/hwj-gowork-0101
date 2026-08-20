package domain

import "time"

// DispositionType 是异常处置类型：让步接收或隔离处置。
type DispositionType string

const (
	DispositionConcession DispositionType = "concession" // 让步接收
	DispositionQuarantine DispositionType = "quarantine" // 隔离处置
)

// DispositionStatus 是处置单状态。
type DispositionStatus string

const (
	DispositionProposed DispositionStatus = "proposed" // 已提出
	DispositionApproved DispositionStatus = "approved" // 已批准
	DispositionExecuted DispositionStatus = "executed" // 已执行
	DispositionRejected DispositionStatus = "rejected" // 已驳回
)

// Disposition 是异常处置单，统一承载让步接收与隔离处置。
// 隔离处置可在批次任意非终态发起并使批次进入 quarantined；
// 让步接收仅允许对初检/复验结论为 fail 的批次提出，批准后执行使批次被接收。
type Disposition struct {
	ID          int64             `json:"id"`
	LotID       int64             `json:"lot_id"`
	Type        DispositionType   `json:"type"`
	Reason      string            `json:"reason"`
	Status      DispositionStatus `json:"status"`
	Resolution  string            `json:"resolution,omitempty"` // 执行方式: scrap/return_to_supplier/concession_accept
	ProposedBy  string            `json:"proposed_by"`
	ApprovedBy  string            `json:"approved_by,omitempty"`
	ExecutedBy  string            `json:"executed_by,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	Version     int64             `json:"version"`
}

// Validate 校验处置单字段。
func (d *Disposition) Validate() error {
	if d.LotID <= 0 {
		return Validation("lot_id", "必须关联来料批次")
	}
	switch d.Type {
	case DispositionConcession, DispositionQuarantine:
	default:
		return Validation("type", "处置类型仅支持 concession/quarantine")
	}
	if d.Reason == "" {
		return Validation("reason", "处置原因不能为空")
	}
	if d.ProposedBy == "" {
		return Validation("proposed_by", "提出人不能为空")
	}
	return nil
}
