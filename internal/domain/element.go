// Package domain 定义金属来料材质符合性判定服务的核心业务模型。
//
// 本包不依赖任何持久化或 HTTP 框架，只包含实体定义、状态机、
// 跨实体业务规则以及统一的分页与错误类型。
package domain

import "encoding/json"

// ElementRange 表示牌号规则中某一化学元素的允许含量区间（质量分数，%）。
type ElementRange struct {
	Element string  `json:"element"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
}

// ElementReading 表示光谱分析报告中某一元素的实测含量（质量分数，%）。
type ElementReading struct {
	Element string  `json:"element"`
	Value   float64 `json:"value"`
}

// RangeViolation 描述一次光谱实测值与牌号区间的偏差。
type RangeViolation struct {
	Element string  `json:"element"`
	Value   float64 `json:"value"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Reason  string  `json:"reason"` // below_min / above_max / missing_reading
}

// ValidateRanges 校验牌号规则元素区间的合法性：
// 元素名非空且不重复、区间下界不大于上界、上下界在 [0,100] 内。
func ValidateRanges(ranges []ElementRange) error {
	if len(ranges) == 0 {
		return Validation("elements", "牌号规则至少包含一个元素区间")
	}
	seen := make(map[string]bool, len(ranges))
	for _, r := range ranges {
		if r.Element == "" {
			return Validation("elements", "元素名称不能为空")
		}
		if seen[r.Element] {
			return Validation("elements", "元素 "+r.Element+" 重复定义")
		}
		seen[r.Element] = true
		if r.Min < 0 || r.Max > 100 || r.Min > r.Max {
			return Validation("elements", "元素 "+r.Element+" 的区间非法")
		}
	}
	return nil
}

// ValidateReadings 校验光谱实测值：元素名非空不重复、数值在 [0,100] 内。
func ValidateReadings(readings []ElementReading) error {
	if len(readings) == 0 {
		return Validation("readings", "光谱报告至少包含一个元素实测值")
	}
	seen := make(map[string]bool, len(readings))
	for _, r := range readings {
		if r.Element == "" {
			return Validation("readings", "元素名称不能为空")
		}
		if seen[r.Element] {
			return Validation("readings", "元素 "+r.Element+" 重复出现")
		}
		seen[r.Element] = true
		if r.Value < 0 || r.Value > 100 {
			return Validation("readings", "元素 "+r.Element+" 的实测值非法")
		}
	}
	return nil
}

// CheckReadingsInRange 将光谱实测值与牌号区间逐一比对。
//
// 规则：规则中列出的每个元素都必须有实测值，且实测值落在 [Min, Max] 内；
// 实测值中出现规则之外的元素不影响判定（余量元素）。
// 返回全部偏差，空切片表示完全符合。
func CheckReadingsInRange(ranges []ElementRange, readings []ElementReading) []RangeViolation {
	byElement := make(map[string]float64, len(readings))
	for _, r := range readings {
		byElement[r.Element] = r.Value
	}
	var violations []RangeViolation
	for _, rg := range ranges {
		v, ok := byElement[rg.Element]
		if !ok {
			violations = append(violations, RangeViolation{
				Element: rg.Element, Min: rg.Min, Max: rg.Max, Reason: "missing_reading",
			})
			continue
		}
		if v < rg.Min {
			violations = append(violations, RangeViolation{
				Element: rg.Element, Value: v, Min: rg.Min, Max: rg.Max, Reason: "below_min",
			})
		} else if v > rg.Max {
			violations = append(violations, RangeViolation{
				Element: rg.Element, Value: v, Min: rg.Min, Max: rg.Max, Reason: "above_max",
			})
		}
	}
	return violations
}

// EncodeRanges 将元素区间序列化为 JSON 文本用于持久化。
func EncodeRanges(ranges []ElementRange) (string, error) {
	b, err := json.Marshal(ranges)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// DecodeRanges 将持久化的 JSON 文本还原为元素区间。
func DecodeRanges(s string) ([]ElementRange, error) {
	var out []ElementRange
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// EncodeReadings 将光谱实测值序列化为 JSON 文本用于持久化。
func EncodeReadings(readings []ElementReading) (string, error) {
	b, err := json.Marshal(readings)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// DecodeReadings 将持久化的 JSON 文本还原为光谱实测值。
func DecodeReadings(s string) ([]ElementReading, error) {
	var out []ElementReading
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, err
	}
	return out, nil
}
