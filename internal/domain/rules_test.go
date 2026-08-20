package domain

import "testing"

func TestRuleStatusTransitions(t *testing.T) {
	statuses := []RuleStatus{RuleStatusDraft, RuleStatusActive, RuleStatusRetired}
	expected := map[RuleStatus]map[RuleStatus]bool{
		RuleStatusDraft:   {RuleStatusActive: true, RuleStatusRetired: true},
		RuleStatusActive:  {RuleStatusRetired: true},
		RuleStatusRetired: {},
	}
	for _, from := range statuses {
		for _, to := range statuses {
			if got := from.CanTransitionTo(to); got != expected[from][to] {
				t.Errorf("RuleStatus.CanTransitionTo(%s -> %s) = %v, 期望 %v", from, to, got, expected[from][to])
			}
		}
	}
}

func assertRule(t *testing.T, err error, rule string) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望规则违反 %s, 得到 nil", rule)
	}
	de := AsDomain(err)
	if de.Code != ErrCodeRuleViolation {
		t.Fatalf("错误码 = %s, 期望 rule_violation", de.Code)
	}
	if de.Rule != rule {
		t.Fatalf("规则编码 = %s, 期望 %s", de.Rule, rule)
	}
}

func TestRequireSupplierExists(t *testing.T) {
	if err := RequireSupplierExists(&Supplier{}); err != nil {
		t.Fatalf("供方存在时不应报错: %v", err)
	}
	assertRule(t, RequireSupplierExists(nil), RuleSupplierMustExist)
}

func TestRequireActiveGradeRule(t *testing.T) {
	assertRule(t, RequireActiveGradeRule(nil, "304"), RuleActiveGradeRuleRequired)
	assertRule(t, RequireActiveGradeRule(&GradeRule{Status: RuleStatusDraft}, "304"), RuleActiveGradeRuleRequired)
	assertRule(t, RequireActiveGradeRule(&GradeRule{Status: RuleStatusRetired}, "304"), RuleActiveGradeRuleRequired)
	if err := RequireActiveGradeRule(&GradeRule{Status: RuleStatusActive}, "304"); err != nil {
		t.Fatalf("active 规则不应报错: %v", err)
	}
}

func TestRequireCertMatchesLot(t *testing.T) {
	lot := &MaterialLot{Grade: "304", HeatNo: "H1"}
	if err := RequireCertMatchesLot(&MillCertificate{Grade: "304", HeatNo: "H1"}, lot); err != nil {
		t.Fatalf("一致时不应报错: %v", err)
	}
	assertRule(t, RequireCertMatchesLot(&MillCertificate{Grade: "316", HeatNo: "H1"}, lot), RuleCertMatchesLot)
	assertRule(t, RequireCertMatchesLot(&MillCertificate{Grade: "304", HeatNo: "H2"}, lot), RuleCertMatchesLot)
}

func TestRequireCertForJudgment(t *testing.T) {
	assertRule(t, RequireCertForJudgment(nil), RuleCertRequiredForJudgment)
	assertRule(t, RequireCertForJudgment(&MillCertificate{Verified: false}), RuleCertRequiredForJudgment)
	if err := RequireCertForJudgment(&MillCertificate{Verified: true}); err != nil {
		t.Fatalf("已核对证明不应报错: %v", err)
	}
}

func TestRequireSpectrumInRange(t *testing.T) {
	if err := RequireSpectrumInRange([]SpectrumReport{
		{ReportNo: "R1", Conclusion: SpectrumInRange},
	}); err != nil {
		t.Fatalf("范围内报告不应报错: %v", err)
	}
	assertRule(t, RequireSpectrumInRange([]SpectrumReport{
		{ReportNo: "R1", Conclusion: SpectrumInRange},
		{ReportNo: "R2", Conclusion: SpectrumOutOfRange},
	}), RuleSpectrumWithinGradeRange)
}

func TestSpectrumAllInRange(t *testing.T) {
	if SpectrumAllInRange(nil) {
		t.Fatal("空报告集不应判定为全部在范围内")
	}
	if !SpectrumAllInRange([]SpectrumReport{{Conclusion: SpectrumInRange}}) {
		t.Fatal("全部在范围内应返回 true")
	}
	if SpectrumAllInRange([]SpectrumReport{{Conclusion: SpectrumInRange}, {Conclusion: SpectrumOutOfRange}}) {
		t.Fatal("存在超范围报告应返回 false")
	}
}

func TestRequireSampleCountComplete(t *testing.T) {
	plan := &SamplingPlan{RequiredCount: 3}
	if err := RequireSampleCountComplete(plan, 3); err != nil {
		t.Fatalf("数量一致不应报错: %v", err)
	}
	assertRule(t, RequireSampleCountComplete(plan, 2), RuleSampleCountComplete)
	assertRule(t, RequireSampleCountComplete(plan, 4), RuleSampleCountComplete)
}

func TestRequireRetestAfterJudgment(t *testing.T) {
	assertRule(t, RequireRetestAfterJudgment(&MaterialLot{}), RuleRetestOnlyAfterJudgment)
	if err := RequireRetestAfterJudgment(&MaterialLot{InitialResult: "fail"}); err != nil {
		t.Fatalf("已判定批次不应报错: %v", err)
	}
}

func TestRequireOriginalSampleRetained(t *testing.T) {
	assertRule(t, RequireOriginalSampleRetained(nil), RuleOriginalSampleRetained)
	assertRule(t, RequireOriginalSampleRetained(&Sample{Kind: SampleKindInitial, Retained: false}), RuleOriginalSampleRetained)
	assertRule(t, RequireOriginalSampleRetained(&Sample{Kind: SampleKindRetest, Retained: true}), RuleOriginalSampleRetained)
	if err := RequireOriginalSampleRetained(&Sample{Kind: SampleKindInitial, Retained: true}); err != nil {
		t.Fatalf("留存原样不应报错: %v", err)
	}
}

func TestRequireCoDecisionForOverride(t *testing.T) {
	if err := RequireCoDecisionForOverride(false, "a", ""); err != nil {
		t.Fatalf("非覆盖不应报错: %v", err)
	}
	assertRule(t, RequireCoDecisionForOverride(true, "a", ""), RuleOverrideRequiresCoDecision)
	assertRule(t, RequireCoDecisionForOverride(true, "a", "a"), RuleOverrideRequiresCoDecision)
	if err := RequireCoDecisionForOverride(true, "a", "b"); err != nil {
		t.Fatalf("不同共同决定人不应报错: %v", err)
	}
}

func TestRequireConcessionForFailure(t *testing.T) {
	assertRule(t, RequireConcessionForFailure(&MaterialLot{InitialResult: "pass"}), RuleConcessionRequiresFailure)
	assertRule(t, RequireConcessionForFailure(&MaterialLot{}), RuleConcessionRequiresFailure)
	if err := RequireConcessionForFailure(&MaterialLot{InitialResult: "fail"}); err != nil {
		t.Fatalf("fail 批次不应报错: %v", err)
	}
	if err := RequireConcessionForFailure(&MaterialLot{InitialResult: "pass", RetestResult: "fail"}); err != nil {
		t.Fatalf("最终结论 fail 不应报错: %v", err)
	}
}

func TestRequireNotQuarantinedForAccept(t *testing.T) {
	assertRule(t, RequireNotQuarantinedForAccept(&MaterialLot{Status: LotStatusQuarantined}), RuleQuarantineBlocksAccept)
	if err := RequireNotQuarantinedForAccept(&MaterialLot{Status: LotStatusJudged}); err != nil {
		t.Fatalf("非隔离批次不应报错: %v", err)
	}
}

func TestRequirePassOrConcessionForAccept(t *testing.T) {
	if err := RequirePassOrConcessionForAccept(&MaterialLot{InitialResult: "pass"}, false); err != nil {
		t.Fatalf("pass 批次不应报错: %v", err)
	}
	assertRule(t, RequirePassOrConcessionForAccept(&MaterialLot{InitialResult: "fail"}, false), RuleAcceptRequiresPassOrConcession)
	if err := RequirePassOrConcessionForAccept(&MaterialLot{InitialResult: "fail"}, true); err != nil {
		t.Fatalf("有让步接收的 fail 批次不应报错: %v", err)
	}
}

func TestErrorHelpers(t *testing.T) {
	e := NotFound("material_lot", 42)
	if e.Code != ErrCodeNotFound || !IsCode(e, ErrCodeNotFound) {
		t.Fatalf("NotFound 构造不符: %+v", e)
	}
	v := VersionConflict("material_lot", 1, 2, 3)
	if v.Code != ErrCodeVersionConflict {
		t.Fatalf("VersionConflict 构造不符: %+v", v)
	}
	d := Duplicate("supplier", "S1")
	if d.Code != ErrCodeDuplicate {
		t.Fatalf("Duplicate 构造不符: %+v", d)
	}
	it := InvalidTransition("material_lot", "a", "b")
	if it.Code != ErrCodeInvalidTransition {
		t.Fatalf("InvalidTransition 构造不符: %+v", it)
	}
	rule := RuleViolation("R00", "msg").WithDetail("k", "v")
	if rule.Details["k"] != "v" || rule.Error() == "" {
		t.Fatalf("RuleViolation/WithDetail 不符: %+v", rule)
	}
	// 非领域错误包装为 internal
	wrapped := AsDomain(errPlain{})
	if wrapped.Code != ErrCodeInternal {
		t.Fatalf("AsDomain 包装不符: %+v", wrapped)
	}
}

type errPlain struct{}

func (errPlain) Error() string { return "plain" }
