package domain

import "time"

// LotStatus 是来料批次主流程状态，共 8 个合法状态。
type LotStatus string

const (
	LotStatusRegistered  LotStatus = "registered"  // 已登记
	LotStatusSampled     LotStatus = "sampled"     // 已取样制样
	LotStatusAnalyzed    LotStatus = "analyzed"    // 光谱分析完成
	LotStatusJudged      LotStatus = "judged"      // 已出具符合性结论
	LotStatusRetesting   LotStatus = "retesting"   // 异议复验中
	LotStatusAccepted    LotStatus = "accepted"    // 已接收（终态）
	LotStatusRejected    LotStatus = "rejected"    // 已拒收（终态）
	LotStatusQuarantined LotStatus = "quarantined" // 隔离中
)

// AllLotStatuses 返回全部合法状态，用于文档与测试遍历。
func AllLotStatuses() []LotStatus {
	return []LotStatus{
		LotStatusRegistered, LotStatusSampled, LotStatusAnalyzed, LotStatusJudged,
		LotStatusRetesting, LotStatusAccepted, LotStatusRejected, LotStatusQuarantined,
	}
}

// lotTransitions 定义主流程状态机的合法转换集合。
var lotTransitions = map[LotStatus][]LotStatus{
	LotStatusRegistered: {LotStatusSampled, LotStatusQuarantined},
	LotStatusSampled:    {LotStatusAnalyzed, LotStatusQuarantined},
	LotStatusAnalyzed:   {LotStatusJudged, LotStatusQuarantined},
	LotStatusJudged:     {LotStatusAccepted, LotStatusRejected, LotStatusRetesting, LotStatusQuarantined},
	LotStatusRetesting:  {LotStatusJudged, LotStatusQuarantined},
	LotStatusRejected:   {LotStatusRetesting}, // 拒收后仍可因异议进入复验
	LotStatusAccepted:   {},                   // 终态
	LotStatusQuarantined: {LotStatusRejected, LotStatusAccepted, LotStatusRetesting},
}

// CanTransitionTo 判断从当前状态到目标状态是否合法。
func (s LotStatus) CanTransitionTo(next LotStatus) bool {
	for _, allowed := range lotTransitions[s] {
		if allowed == next {
			return true
		}
	}
	return false
}

// MustTransitionTo 校验状态转换，非法时返回领域错误。
func (s LotStatus) MustTransitionTo(next LotStatus) error {
	if !s.CanTransitionTo(next) {
		return InvalidTransition("material_lot", string(s), string(next))
	}
	return nil
}

// IsTerminal 判断是否为终态（accepted / rejected 之外的终止视为非终态，
// 因为 rejected 仍可进入复验）。
func (s LotStatus) IsTerminal() bool {
	return s == LotStatusAccepted
}

// MaterialLot 是来料批次，承载主流程状态机与炉批号、牌号等关键标识。
type MaterialLot struct {
	ID            int64      `json:"id"`
	LotNo         string     `json:"lot_no"` // 来料批次号，全局唯一，幂等自然键
	SupplierID    int64      `json:"supplier_id"`
	HeatNo        string     `json:"heat_no"` // 炉批号
	Grade         string     `json:"grade"`   // 牌号
	Quantity      float64    `json:"quantity"`
	Status        LotStatus  `json:"status"`
	InitialResult string     `json:"initial_result"` // 初检结论: pass/fail，未判定为空
	RetestResult  string     `json:"retest_result"`  // 复验结论: pass/fail，未复验为空
	AcceptedBy    string     `json:"accepted_by,omitempty"`
	AcceptedAt    *time.Time `json:"accepted_at,omitempty"`
	ReceivedAt    time.Time  `json:"received_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	Version       int64      `json:"version"`
}

// Validate 校验来料批次字段。
func (l *MaterialLot) Validate() error {
	if l.LotNo == "" {
		return Validation("lot_no", "来料批次号不能为空")
	}
	if len(l.LotNo) > 64 {
		return Validation("lot_no", "来料批次号长度不能超过 64")
	}
	if l.SupplierID <= 0 {
		return Validation("supplier_id", "必须指定有效供方")
	}
	if l.HeatNo == "" {
		return Validation("heat_no", "炉批号不能为空")
	}
	if l.Grade == "" {
		return Validation("grade", "牌号不能为空")
	}
	if l.Quantity <= 0 {
		return Validation("quantity", "数量必须为正数")
	}
	return nil
}

// FinalResult 返回批次当前生效的结论：复验结论优先于初检结论。
// 复验结论一旦出具即覆盖初检结论，因此接收判定与让步校验统一取当前结论。
func (l *MaterialLot) FinalResult() string {
	if l.RetestResult != "" {
		return l.RetestResult
	}
	return l.InitialResult
}
