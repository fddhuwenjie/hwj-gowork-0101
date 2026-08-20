package domain

import "time"

// PlanStatus 是取样计划状态。
type PlanStatus string

const (
	PlanStatusActive    PlanStatus = "active"    // 已生效，可登记样本
	PlanStatusCompleted PlanStatus = "completed" // 取样完成
	PlanStatusCancelled PlanStatus = "cancelled" // 已作废（批次被隔离）
)

// SamplingPlan 是取样计划：一个来料批次对应一份计划，
// 规定应取样数量与留样位置（留样位置是异议复验的前提）。
type SamplingPlan struct {
	ID             int64      `json:"id"`
	PlanNo         string     `json:"plan_no"` // 计划编号，全局唯一，幂等自然键
	LotID          int64      `json:"lot_id"`
	RequiredCount  int        `json:"required_count"`  // 应取样数量
	RetainLocation string     `json:"retain_location"` // 留样位置
	Status         PlanStatus `json:"status"`
	CreatedBy      string     `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	Version        int64      `json:"version"`
}

// Validate 校验取样计划字段。
func (p *SamplingPlan) Validate() error {
	if p.PlanNo == "" {
		return Validation("plan_no", "取样计划编号不能为空")
	}
	if p.LotID <= 0 {
		return Validation("lot_id", "必须关联来料批次")
	}
	if p.RequiredCount <= 0 || p.RequiredCount > 20 {
		return Validation("required_count", "应取样数量须在 1-20 之间")
	}
	if p.RetainLocation == "" {
		return Validation("retain_location", "留样位置不能为空")
	}
	return nil
}
