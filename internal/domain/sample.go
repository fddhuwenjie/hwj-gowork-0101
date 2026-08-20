package domain

import "time"

// SampleKind 区分初检样本与复验样本。
type SampleKind string

const (
	SampleKindInitial SampleKind = "initial"
	SampleKindRetest  SampleKind = "retest"
)

// SampleStatus 是样本状态。
type SampleStatus string

const (
	SampleStatusCreated  SampleStatus = "created"  // 已制样
	SampleStatusTested   SampleStatus = "tested"   // 已出光谱报告
	SampleStatusConsumed SampleStatus = "consumed" // 已消耗（破坏性复验）
)

// Sample 是取样样本。(PlanID, SampleNo) 唯一，作为幂等自然键。
// Retained 标记原样是否留存，异议复验必须使用留样。
type Sample struct {
	ID        int64        `json:"id"`
	PlanID    int64        `json:"plan_id"`
	SampleNo  string       `json:"sample_no"`
	Kind      SampleKind   `json:"kind"`
	Retained  bool         `json:"retained"`
	Status    SampleStatus `json:"status"`
	CreatedAt time.Time    `json:"created_at"`
	Version   int64        `json:"version"`
}

// Validate 校验样本字段。
func (s *Sample) Validate() error {
	if s.PlanID <= 0 {
		return Validation("plan_id", "必须关联取样计划")
	}
	if s.SampleNo == "" {
		return Validation("sample_no", "样本编号不能为空")
	}
	switch s.Kind {
	case SampleKindInitial, SampleKindRetest:
	default:
		return Validation("kind", "样本类型仅支持 initial/retest")
	}
	return nil
}
