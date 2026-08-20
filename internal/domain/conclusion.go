package domain

import "time"

// ConclusionRound 区分初检结论与复验结论。
type ConclusionRound string

const (
	RoundInitial ConclusionRound = "initial"
	RoundRetest  ConclusionRound = "retest"
)

// ConclusionResult 是符合性结论结果。
type ConclusionResult string

const (
	ResultPass ConclusionResult = "pass"
	ResultFail ConclusionResult = "fail"
)

// ConformityConclusion 是批次的符合性结论，初检与复验各最多一条。
// (LotID, Round) 唯一。复验结论覆盖初检结论时必须记录共同决定人。
type ConformityConclusion struct {
	ID            int64            `json:"id"`
	LotID         int64            `json:"lot_id"`
	Round         ConclusionRound  `json:"round"`
	Result        ConclusionResult `json:"result"`
	CertOK        bool             `json:"cert_ok"`     // 材质证明核对是否通过
	SpectrumOK    bool             `json:"spectrum_ok"` // 光谱结果是否全部在牌号区间
	Reason        string           `json:"reason"`
	DecidedBy     string           `json:"decided_by"`
	CoDecidedBy   string           `json:"co_decided_by,omitempty"` // 覆盖初检时的共同决定人
	OverridesPrev bool             `json:"overrides_prev"`          // 是否覆盖了上一轮结论
	CreatedAt     time.Time        `json:"created_at"`
	Version       int64            `json:"version"`
}

// Validate 校验结论字段。
func (c *ConformityConclusion) Validate() error {
	if c.LotID <= 0 {
		return Validation("lot_id", "必须关联来料批次")
	}
	switch c.Round {
	case RoundInitial, RoundRetest:
	default:
		return Validation("round", "结论轮次仅支持 initial/retest")
	}
	switch c.Result {
	case ResultPass, ResultFail:
	default:
		return Validation("result", "结论结果仅支持 pass/fail")
	}
	if c.DecidedBy == "" {
		return Validation("decided_by", "判定人不能为空")
	}
	return nil
}
