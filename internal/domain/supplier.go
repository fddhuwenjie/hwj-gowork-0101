package domain

import "time"

// Supplier 是供方（供应商），来料批次必须归属一个已存在的供方。
type Supplier struct {
	ID        int64     `json:"id"`
	Code      string    `json:"code"` // 供方编码，全局唯一，作为幂等自然键
	Name      string    `json:"name"`
	Contact   string    `json:"contact"`
	CreatedAt time.Time `json:"created_at"`
	Version   int64     `json:"version"`
}

// Validate 校验供方字段。
func (s *Supplier) Validate() error {
	if s.Code == "" {
		return Validation("code", "供方编码不能为空")
	}
	if len(s.Code) > 32 {
		return Validation("code", "供方编码长度不能超过 32")
	}
	if s.Name == "" {
		return Validation("name", "供方名称不能为空")
	}
	if len(s.Name) > 128 {
		return Validation("name", "供方名称长度不能超过 128")
	}
	return nil
}
