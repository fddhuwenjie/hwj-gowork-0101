package domain

import "time"

// SpectrumConclusion 是单份光谱报告的符合性结论。
type SpectrumConclusion string

const (
	SpectrumInRange    SpectrumConclusion = "in_range"     // 全部元素落在牌号区间
	SpectrumOutOfRange SpectrumConclusion = "out_of_range" // 存在元素超出牌号区间
)

// SpectrumReport 是光谱分析报告，针对一个样本出具。
// ReportNo 全局唯一，作为幂等自然键；一个样本仅允许一份报告。
// 结论在提交时依据批次牌号的 active 规则版本计算并固化，保证可追溯。
type SpectrumReport struct {
	ID           int64             `json:"id"`
	ReportNo     string            `json:"report_no"`
	SampleID     int64             `json:"sample_id"`
	RuleID       int64             `json:"rule_id"` // 判定时引用的规则版本
	Readings     []ElementReading  `json:"readings"`
	Violations   []RangeViolation  `json:"violations"`
	Conclusion   SpectrumConclusion `json:"conclusion"`
	Analyzer     string            `json:"analyzer"`
	CreatedAt    time.Time         `json:"created_at"`
	Version      int64             `json:"version"`
}

// Validate 校验光谱报告字段。
func (r *SpectrumReport) Validate() error {
	if r.ReportNo == "" {
		return Validation("report_no", "光谱报告编号不能为空")
	}
	if r.SampleID <= 0 {
		return Validation("sample_id", "必须关联样本")
	}
	if r.Analyzer == "" {
		return Validation("analyzer", "分析员不能为空")
	}
	return ValidateReadings(r.Readings)
}
