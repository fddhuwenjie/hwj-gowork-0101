package domain

import "time"

// LotFilter 是来料批次列表的过滤条件，零值字段不参与过滤。
type LotFilter struct {
	Status        LotStatus
	SupplierID    int64
	Grade         string
	LotNoPrefix   string
	ReceivedAfter *time.Time
	ReceivedBefore *time.Time
}

// SupplierFilter 是供方列表过滤条件。
type SupplierFilter struct {
	CodePrefix string
	NameLike   string
}

// GradeRuleFilter 是牌号规则列表过滤条件。
type GradeRuleFilter struct {
	Grade  string
	Status RuleStatus
}

// AuditFilter 是审计事件列表过滤条件。
type AuditFilter struct {
	Entity   string
	EntityID int64
	Actor    string
	Since    *time.Time
}

// JobFilter 是后台任务列表过滤条件。
type JobFilter struct {
	Status JobStatus
	Type   string
}

// RetestFilter 是复验任务列表过滤条件。
type RetestFilter struct {
	LotID  int64
	Status RetestStatus
}

// DispositionFilter 是处置单列表过滤条件。
type DispositionFilter struct {
	LotID  int64
	Type   DispositionType
	Status DispositionStatus
}

// RetestAcceptedRow 是派生查询一的行：
// 初检不符合但复验仍接收的批次及其材质证明编号。
type RetestAcceptedRow struct {
	LotID       int64     `json:"lot_id"`
	LotNo       string    `json:"lot_no"`
	SupplierCode string   `json:"supplier_code"`
	Grade       string    `json:"grade"`
	HeatNo      string    `json:"heat_no"`
	CertNo      string    `json:"cert_no"`
	DecidedBy   string    `json:"decided_by"`
	CoDecidedBy string    `json:"co_decided_by"`
	AcceptedAt  time.Time `json:"accepted_at"`
}

// CertMissingAcceptedRow 是派生查询二的行：
// 各供方近期材质证明缺失而先接收的批次数量。
type CertMissingAcceptedRow struct {
	SupplierID   int64  `json:"supplier_id"`
	SupplierCode string `json:"supplier_code"`
	SupplierName string `json:"supplier_name"`
	LotCount     int64  `json:"lot_count"`
}
