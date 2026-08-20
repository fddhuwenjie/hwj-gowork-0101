package httpapi

import (
	"time"

	"metalmics/internal/domain"
)

// supplierReq 是登记供方请求。
type supplierReq struct {
	Code    string `json:"code"`
	Name    string `json:"name"`
	Contact string `json:"contact"`
}

// gradeRuleReq 是创建牌号规则版本请求。
type gradeRuleReq struct {
	Grade     string                `json:"grade"`
	VersionNo int                   `json:"version_no"`
	Elements  []domain.ElementRange `json:"elements"`
	Remark    string                `json:"remark"`
}

// versionReq 是仅携带乐观锁版本的通用请求。
type versionReq struct {
	Version int64 `json:"version"`
}

// lotReq 是来料登记请求。
type lotReq struct {
	LotNo      string    `json:"lot_no"`
	SupplierID int64     `json:"supplier_id"`
	HeatNo     string    `json:"heat_no"`
	Grade      string    `json:"grade"`
	Quantity   float64   `json:"quantity"`
	ReceivedAt time.Time `json:"received_at"`
}

// samplingPlanReq 是制定取样计划请求。
type samplingPlanReq struct {
	PlanNo         string `json:"plan_no"`
	RequiredCount  int    `json:"required_count"`
	RetainLocation string `json:"retain_location"`
}

// sampleItem 是批量登记样本的单项。
type sampleItem struct {
	SampleNo string `json:"sample_no"`
	Retained bool   `json:"retained"`
}

// samplesReq 是批量登记样本请求。
type samplesReq struct {
	Samples []sampleItem `json:"samples"`
}

// spectrumReq 是提交光谱报告请求。
type spectrumReq struct {
	ReportNo string                  `json:"report_no"`
	Readings []domain.ElementReading `json:"readings"`
	Analyzer string                  `json:"analyzer"`
}

// certificateReq 是登记材质证明请求。
type certificateReq struct {
	CertNo   string                `json:"cert_no"`
	Grade    string                `json:"grade"`
	HeatNo   string                `json:"heat_no"`
	Elements []domain.ElementRange `json:"elements"`
	IssuedAt time.Time             `json:"issued_at"`
}

// judgeReq 是符合性判定请求。
type judgeReq struct {
	Version   int64  `json:"version"`
	DecidedBy string `json:"decided_by"`
	Reason    string `json:"reason"`
}

// retestReq 是异议复验申请请求。
type retestReq struct {
	SampleID int64  `json:"sample_id"`
	Reason   string `json:"reason"`
}

// concludeRetestReq 是出具复验结论请求。
type concludeRetestReq struct {
	Version     int64  `json:"version"`
	Result      string `json:"result"`
	DecidedBy   string `json:"decided_by"`
	CoDecidedBy string `json:"co_decided_by"`
	Reason      string `json:"reason"`
}

// dispositionReq 是提出处置单请求。
type dispositionReq struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

// executeDispositionReq 是执行处置单请求。
type executeDispositionReq struct {
	Version    int64  `json:"version"`
	Resolution string `json:"resolution"`
}

// batchAcceptReq 是批量接收请求。
type batchAcceptReq struct {
	LotIDs []int64 `json:"lot_ids"`
}

// jobReq 是入队后台任务请求。
type jobReq struct {
	Type        string                 `json:"type"`
	Payload     map[string]interface{} `json:"payload"`
	MaxAttempts int                    `json:"max_attempts"`
}

// createdResp 是创建类端点的统一响应，replayed 标记幂等重放。
type createdResp struct {
	Data     interface{} `json:"data"`
	Replayed bool        `json:"replayed"`
}

// okResp 是简单成功响应。
type okResp struct {
	Data interface{} `json:"data"`
}
