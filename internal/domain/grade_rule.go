package domain

import "time"

// RuleStatus 是牌号规则版本的生命周期状态。
type RuleStatus string

const (
	RuleStatusDraft   RuleStatus = "draft"   // 草稿，可编辑，不可用于判定
	RuleStatusActive  RuleStatus = "active"  // 生效中，同一牌号同一时刻最多一个
	RuleStatusRetired RuleStatus = "retired" // 已废止，仅作历史追溯
)

// CanTransitionTo 定义规则状态的合法转换：draft->active/retired, active->retired。
func (s RuleStatus) CanTransitionTo(next RuleStatus) bool {
	switch s {
	case RuleStatusDraft:
		return next == RuleStatusActive || next == RuleStatusRetired
	case RuleStatusActive:
		return next == RuleStatusRetired
	default:
		return false
	}
}

// GradeRule 是某牌号某版本的元素含量区间规则。
// (Grade, VersionNo) 全局唯一，作为幂等自然键；判定必须引用 active 版本。
type GradeRule struct {
	ID        int64          `json:"id"`
	Grade     string         `json:"grade"`      // 牌号，如 304 / 42CrMo
	VersionNo int            `json:"version_no"` // 规则版本号，从 1 递增
	Elements  []ElementRange `json:"elements"`
	Status    RuleStatus     `json:"status"`
	Remark    string         `json:"remark"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	Version   int64          `json:"version"`
}

// Validate 校验牌号规则字段。
func (r *GradeRule) Validate() error {
	if r.Grade == "" {
		return Validation("grade", "牌号不能为空")
	}
	if len(r.Grade) > 32 {
		return Validation("grade", "牌号长度不能超过 32")
	}
	if r.VersionNo <= 0 {
		return Validation("version_no", "规则版本号必须为正整数")
	}
	if err := ValidateRanges(r.Elements); err != nil {
		return err
	}
	return nil
}
