package domain

import "time"

// MillCertificate 是供方随货提供的材质证明（质保书）。
// CertNo 全局唯一，作为幂等自然键；判定前必须登记且核对通过。
type MillCertificate struct {
	ID         int64          `json:"id"`
	CertNo     string         `json:"cert_no"`
	LotID      int64          `json:"lot_id"`
	Grade      string         `json:"grade"`
	HeatNo     string         `json:"heat_no"`
	Elements   []ElementRange `json:"elements"` // 证明书载明的实测值（复用区间结构表达上下限声明）
	IssuedAt   time.Time      `json:"issued_at"`
	Verified   bool           `json:"verified"`
	VerifiedBy string         `json:"verified_by,omitempty"`
	VerifiedAt *time.Time     `json:"verified_at,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	Version    int64          `json:"version"`
}

// Validate 校验材质证明字段。
func (c *MillCertificate) Validate() error {
	if c.CertNo == "" {
		return Validation("cert_no", "材质证明编号不能为空")
	}
	if len(c.CertNo) > 64 {
		return Validation("cert_no", "材质证明编号长度不能超过 64")
	}
	if c.LotID <= 0 {
		return Validation("lot_id", "必须关联来料批次")
	}
	if c.Grade == "" {
		return Validation("grade", "材质证明牌号不能为空")
	}
	if c.HeatNo == "" {
		return Validation("heat_no", "材质证明炉批号不能为空")
	}
	return nil
}
