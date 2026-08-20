package domain

import "time"

// AuditEvent 是审计记录：每一次改变业务实体状态的操作都必须在
// 同一事务内追加一条审计事件，保证任何状态流转可追溯。
type AuditEvent struct {
	ID        int64     `json:"id"`
	Entity    string    `json:"entity"` // 实体类型，如 material_lot / retest_task
	EntityID  int64     `json:"entity_id"`
	Action    string    `json:"action"` // 动作，如 register / judge / accept
	Actor     string    `json:"actor"`  // 操作人
	Detail    string    `json:"detail"` // JSON 明细
	CreatedAt time.Time `json:"created_at"`
}

// Validate 校验审计事件字段。
func (a *AuditEvent) Validate() error {
	if a.Entity == "" {
		return Validation("entity", "审计实体类型不能为空")
	}
	if a.Action == "" {
		return Validation("action", "审计动作不能为空")
	}
	if a.Actor == "" {
		return Validation("actor", "审计操作人不能为空")
	}
	return nil
}
