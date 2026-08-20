package domain

import "fmt"

// 跨实体业务规则编码。每条规则在规则违反时会以 rule_violation 错误返回，
// 规则编码用于审计、文档与测试断言。
const (
	RuleSupplierMustExist        = "R01_supplier_must_exist"
	RuleActiveGradeRuleRequired  = "R02_active_grade_rule_required"
	RuleCertMatchesLot           = "R03_cert_matches_lot"
	RuleCertRequiredForJudgment  = "R04_cert_required_for_judgment"
	RuleSpectrumWithinGradeRange = "R05_spectrum_within_grade_range"
	RuleSampleCountComplete      = "R06_sample_count_complete"
	RuleRetestOnlyAfterJudgment  = "R07_retest_only_after_judgment"
	RuleOriginalSampleRetained   = "R08_original_sample_retained"
	RuleOverrideRequiresCoDecision = "R09_override_requires_co_decision"
	RuleConcessionRequiresFailure  = "R10_concession_requires_failure"
	RuleQuarantineBlocksAccept     = "R11_quarantine_blocks_accept"
	RuleAcceptRequiresPassOrConcession = "R12_accept_requires_pass_or_concession"
)

// RequireSupplierExists R01: 来料批次必须归属已存在的供方。
func RequireSupplierExists(s *Supplier) error {
	if s == nil {
		return RuleViolation(RuleSupplierMustExist, "供方不存在，不允许登记来料批次")
	}
	return nil
}

// RequireActiveGradeRule R02: 登记与判定必须存在该牌号的 active 规则版本。
func RequireActiveGradeRule(rule *GradeRule, grade string) error {
	if rule == nil || rule.Status != RuleStatusActive {
		return RuleViolation(RuleActiveGradeRuleRequired,
			fmt.Sprintf("牌号 %s 无生效中的规则版本", grade))
	}
	return nil
}

// RequireCertMatchesLot R03: 材质证明的牌号与炉批号必须与批次一致。
func RequireCertMatchesLot(cert *MillCertificate, lot *MaterialLot) error {
	if cert.Grade != lot.Grade {
		return RuleViolation(RuleCertMatchesLot,
			fmt.Sprintf("材质证明牌号 %s 与批次牌号 %s 不一致", cert.Grade, lot.Grade))
	}
	if cert.HeatNo != lot.HeatNo {
		return RuleViolation(RuleCertMatchesLot,
			fmt.Sprintf("材质证明炉批号 %s 与批次炉批号 %s 不一致", cert.HeatNo, lot.HeatNo))
	}
	return nil
}

// RequireCertForJudgment R04: 无材质证明（或未核对通过）不得判定。
func RequireCertForJudgment(cert *MillCertificate) error {
	if cert == nil {
		return RuleViolation(RuleCertRequiredForJudgment, "无材质证明，不得判定")
	}
	if !cert.Verified {
		return RuleViolation(RuleCertRequiredForJudgment, "材质证明尚未核对通过，不得判定")
	}
	return nil
}

// RequireSpectrumInRange R05: 光谱分析结果必须与牌号范围一致。
// 任意一份报告存在偏差即违反；偏差明细附在错误 Details 中。
func RequireSpectrumInRange(reports []SpectrumReport) error {
	for _, r := range reports {
		if r.Conclusion == SpectrumOutOfRange {
			return RuleViolation(RuleSpectrumWithinGradeRange,
				fmt.Sprintf("光谱报告 %s 存在超出牌号范围的元素", r.ReportNo))
		}
	}
	return nil
}

// SpectrumAllInRange 判定一组光谱报告是否全部在范围内（供判定流程计算结论用，
// 与 R05 的强制校验不同，本函数只返回布尔值）。
func SpectrumAllInRange(reports []SpectrumReport) bool {
	for _, r := range reports {
		if r.Conclusion != SpectrumInRange {
			return false
		}
	}
	return len(reports) > 0
}

// RequireSampleCountComplete R06: 取样完成前，已登记初检样本数必须等于计划数量。
func RequireSampleCountComplete(plan *SamplingPlan, registered int) error {
	if registered != plan.RequiredCount {
		return RuleViolation(RuleSampleCountComplete,
			fmt.Sprintf("计划取样 %d 个，已登记 %d 个，不允许完成取样", plan.RequiredCount, registered))
	}
	return nil
}

// RequireRetestAfterJudgment R07: 异议复验只能在已判定（含拒收）之后发起。
func RequireRetestAfterJudgment(lot *MaterialLot) error {
	if lot.InitialResult == "" {
		return RuleViolation(RuleRetestOnlyAfterJudgment, "批次尚未出具初检结论，不允许发起异议复验")
	}
	return nil
}

// RequireOriginalSampleRetained R08: 复验必须使用留存的初检原样。
func RequireOriginalSampleRetained(sample *Sample) error {
	if sample == nil {
		return RuleViolation(RuleOriginalSampleRetained, "复验样本不存在")
	}
	if sample.Kind != SampleKindInitial || !sample.Retained {
		return RuleViolation(RuleOriginalSampleRetained, "异议必须保留原样复验：所选样本不是留存的初检样本")
	}
	return nil
}

// RequireCoDecisionForOverride R09: 复验结论覆盖初检结论时必须由两人共同决定，
// 且共同决定人不能与判定人为同一人。
func RequireCoDecisionForOverride(override bool, decidedBy, coDecidedBy string) error {
	if !override {
		return nil
	}
	if coDecidedBy == "" {
		return RuleViolation(RuleOverrideRequiresCoDecision,
			"复验结论覆盖初检结论，必须共同决定（缺少共同决定人）")
	}
	if coDecidedBy == decidedBy {
		return RuleViolation(RuleOverrideRequiresCoDecision,
			"共同决定人不能与判定人为同一人")
	}
	return nil
}

// RequireConcessionForFailure R10: 让步接收仅允许对最终结论为 fail 的批次提出。
func RequireConcessionForFailure(lot *MaterialLot) error {
	if lot.FinalResult() != string(ResultFail) {
		return RuleViolation(RuleConcessionRequiresFailure,
			"仅结论为不符合的批次才允许申请让步接收")
	}
	return nil
}

// RequireNotQuarantinedForAccept R11: 隔离中的批次不得直接接收，
// 必须先通过处置单执行 concession_accept 解除。
func RequireNotQuarantinedForAccept(lot *MaterialLot) error {
	if lot.Status == LotStatusQuarantined {
		return RuleViolation(RuleQuarantineBlocksAccept,
			"批次处于隔离状态，不得直接接收，请先完成隔离处置")
	}
	return nil
}

// RequirePassOrConcessionForAccept R12: 接收必须满足最终结论 pass，
// 或存在已批准的让步接收处置单。
func RequirePassOrConcessionForAccept(lot *MaterialLot, hasApprovedConcession bool) error {
	if lot.FinalResult() == string(ResultPass) {
		return nil
	}
	if !hasApprovedConcession {
		return RuleViolation(RuleAcceptRequiresPassOrConcession,
			"最终结论不符合且无已批准的让步接收，不允许接收")
	}
	return nil
}
