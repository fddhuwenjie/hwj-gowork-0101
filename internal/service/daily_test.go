package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"metalmics/internal/domain"
	"metalmics/internal/repository"
)

// TestDailyFlow_EndToEnd 覆盖完整日常流程：
// 供方→规则→激活→登记批次→取样计划→样本→取样完成→光谱报告→analyze→材质证明→核对→判定→接收。
func TestDailyFlow_EndToEnd(t *testing.T) {
	env := newTestEnv(t)

	// 供方与 active 规则已由 newTestEnv 建立；这里验证规则版本替换
	ruleV2 := &domain.GradeRule{Grade: "304", VersionNo: 2, Elements: testElements, Remark: "v2"}
	if _, err := env.daily.CreateGradeRule(env.ctx, ruleV2, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.daily.ActivateGradeRule(env.ctx, ruleV2.ID, ruleV2.Version, "tester"); err != nil {
		t.Fatal(err)
	}
	v1, err := repository.NewGradeRuleRepo(env.db).GetByGradeVersion(env.ctx, "304", 1)
	if err != nil || v1 == nil || v1.Status != domain.RuleStatusRetired {
		t.Fatalf("旧版本应被废止: %+v err=%v", v1, err)
	}

	lot := env.mustRegisterLot(t, "L-E2E", "304")
	if lot.Status != domain.LotStatusRegistered || lot.Version != 1 {
		t.Fatalf("登记后状态不符: %+v", lot)
	}

	samples := env.mustSampled(t, lot.ID, "P-E2E", 2)
	lot = env.getLot(t, lot.ID)
	if lot.Status != domain.LotStatusSampled {
		t.Fatalf("取样完成后应为 sampled: %s", lot.Status)
	}

	env.mustAnalyzed(t, lot.ID, samples, "R-E2E", true)
	lot = env.getLot(t, lot.ID)
	if lot.Status != domain.LotStatusAnalyzed {
		t.Fatalf("分析后应为 analyzed: %s", lot.Status)
	}

	env.mustVerifiedCert(t, lot, "C-E2E")

	conclusion, err := env.daily.JudgeLot(env.ctx, lot.ID, 0, "judge1", "例行判定")
	if err != nil {
		t.Fatalf("判定失败: %v", err)
	}
	if conclusion.Result != domain.ResultPass || !conclusion.CertOK || !conclusion.SpectrumOK {
		t.Fatalf("判定结论不符: %+v", conclusion)
	}
	lot = env.getLot(t, lot.ID)
	if lot.Status != domain.LotStatusJudged || lot.InitialResult != "pass" {
		t.Fatalf("判定后状态不符: %+v", lot)
	}

	accepted, err := env.daily.AcceptLot(env.ctx, lot.ID, lot.Version, "receiver")
	if err != nil {
		t.Fatalf("接收失败: %v", err)
	}
	if accepted.Status != domain.LotStatusAccepted || accepted.AcceptedBy != "receiver" || accepted.AcceptedAt == nil {
		t.Fatalf("接收结果不符: %+v", accepted)
	}

	// 聚合详情
	detail, err := env.daily.GetLotDetail(env.ctx, lot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Samples) != 2 || len(detail.Reports) != 2 || len(detail.Certificates) != 1 || len(detail.Conclusions) != 1 {
		t.Fatalf("聚合详情不符: %+v", detail)
	}

	// 每个状态变更操作都应留有审计
	audits, err := env.report.ListAuditEvents(env.ctx, domain.AuditFilter{Entity: "material_lot", EntityID: lot.ID},
		domain.PageRequest{Page: 1, PageSize: 100, Sort: "id", Order: "asc"})
	if err != nil {
		t.Fatal(err)
	}
	wantActions := []string{"register", "complete_sampling", "analyze", "judge", "accept"}
	if audits.Total != int64(len(wantActions)) {
		t.Fatalf("批次审计事件数 = %d, 期望 %d", audits.Total, len(wantActions))
	}
	for i, a := range audits.Items {
		if a.Action != wantActions[i] {
			t.Fatalf("审计动作[%d] = %s, 期望 %s", i, a.Action, wantActions[i])
		}
	}
}

// TestRegisterLot_Idempotent 重复登记同 lot_no 返回 created=false 且数据一致。
func TestRegisterLot_Idempotent(t *testing.T) {
	env := newTestEnv(t)
	lot := env.mustRegisterLot(t, "L-IDEM", "304")

	replay := &domain.MaterialLot{
		LotNo: "L-IDEM", SupplierID: env.supplier.ID, HeatNo: "H-L-IDEM", Grade: "304",
		Quantity: 10, ReceivedAt: time.Now().UTC(),
	}
	created, err := env.daily.RegisterLot(env.ctx, replay, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("重复登记应 created=false")
	}
	if replay.ID != lot.ID || replay.LotNo != lot.LotNo || replay.HeatNo != lot.HeatNo || replay.Status != lot.Status {
		t.Fatalf("幂等重放数据不一致: %+v vs %+v", replay, lot)
	}
}

// TestRegisterLot_R02 无 active 规则不能登记批次。
func TestRegisterLot_R02(t *testing.T) {
	env := newTestEnv(t)
	lot := &domain.MaterialLot{
		LotNo: "L-NORULE", SupplierID: env.supplier.ID, HeatNo: "H1", Grade: "NOPE",
		Quantity: 1, ReceivedAt: time.Now().UTC(),
	}
	_, err := env.daily.RegisterLot(env.ctx, lot, "tester")
	de := domain.AsDomain(err)
	if de.Code != domain.ErrCodeRuleViolation || de.Rule != domain.RuleActiveGradeRuleRequired {
		t.Fatalf("期望 R02 规则违反, 实际 %v", err)
	}
}

// TestRegisterLot_RollbackOnMissingSupplier 供方不存在时整体回滚，无部分写入。
func TestRegisterLot_RollbackOnMissingSupplier(t *testing.T) {
	env := newTestEnv(t)
	lot := &domain.MaterialLot{
		LotNo: "L-GHOST", SupplierID: 99999, HeatNo: "H1", Grade: "304",
		Quantity: 1, ReceivedAt: time.Now().UTC(),
	}
	_, err := env.daily.RegisterLot(env.ctx, lot, "tester")
	if err == nil {
		t.Fatal("供方不存在应报错")
	}
	got, gerr := repository.NewLotRepo(env.db).GetByLotNo(env.ctx, "L-GHOST")
	if gerr != nil || got != nil {
		t.Fatalf("回滚后不应存在部分写入: %v %v", got, gerr)
	}
	// 也不应写入任何审计
	audits, _ := env.report.ListAuditEvents(env.ctx, domain.AuditFilter{Entity: "material_lot"},
		domain.PageRequest{Page: 1, PageSize: 10, Sort: "id", Order: "asc"})
	if audits.Total != 0 {
		t.Fatalf("回滚后不应有批次审计: %d", audits.Total)
	}
}

// TestCompleteSampling_R06 样本数不足不能完成取样。
func TestCompleteSampling_R06(t *testing.T) {
	env := newTestEnv(t)
	lot := env.mustRegisterLot(t, "L-R06", "304")
	plan := &domain.SamplingPlan{PlanNo: "P-R06", LotID: lot.ID, RequiredCount: 2, RetainLocation: "柜A", CreatedBy: "tester"}
	if _, err := env.daily.CreateSamplingPlan(env.ctx, plan, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.daily.RegisterSamples(env.ctx, plan.ID, []*domain.Sample{{SampleNo: "S1", Retained: true}}, "tester"); err != nil {
		t.Fatal(err)
	}
	_, err := env.daily.CompleteSampling(env.ctx, lot.ID, 0, "tester")
	de := domain.AsDomain(err)
	if de.Code != domain.ErrCodeRuleViolation || de.Rule != domain.RuleSampleCountComplete {
		t.Fatalf("期望 R06 规则违反, 实际 %v", err)
	}
	// 超量登记同样被 R06 拦截
	_, err = env.daily.RegisterSamples(env.ctx, plan.ID, []*domain.Sample{{SampleNo: "S2"}, {SampleNo: "S3"}}, "tester")
	de = domain.AsDomain(err)
	if de.Code != domain.ErrCodeRuleViolation || de.Rule != domain.RuleSampleCountComplete {
		t.Fatalf("超量登记期望 R06, 实际 %v", err)
	}
}

// TestRegisterSamples_IdempotentReplay 同一样本编号重复登记视为重放跳过。
func TestRegisterSamples_IdempotentReplay(t *testing.T) {
	env := newTestEnv(t)
	lot := env.mustRegisterLot(t, "L-SMP", "304")
	plan := &domain.SamplingPlan{PlanNo: "P-SMP", LotID: lot.ID, RequiredCount: 2, RetainLocation: "柜A", CreatedBy: "tester"}
	if _, err := env.daily.CreateSamplingPlan(env.ctx, plan, "tester"); err != nil {
		t.Fatal(err)
	}
	samples := []*domain.Sample{{SampleNo: "S1", Retained: true}, {SampleNo: "S1", Retained: true}}
	inserted, err := env.daily.RegisterSamples(env.ctx, plan.ID, samples, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if inserted != 1 {
		t.Fatalf("重复样本应跳过, inserted = %d", inserted)
	}
}

// TestCompleteSampling_VersionConflict 错误 expectedVersion 返回 version_conflict。
func TestCompleteSampling_VersionConflict(t *testing.T) {
	env := newTestEnv(t)
	lot := env.mustRegisterLot(t, "L-VC", "304")
	env.mustSampled(t, lot.ID, "P-VC", 1)

	lot = env.getLot(t, lot.ID)
	if _, err := env.daily.CompleteSampling(env.ctx, lot.ID, lot.Version+99, "tester"); !domain.IsCode(err, domain.ErrCodeVersionConflict) {
		t.Fatalf("期望 version_conflict, 实际 %v", err)
	}
}

// TestAnalyzeLot_RequiresAllReports 尚有样本未出报告时不能完成分析。
func TestAnalyzeLot_RequiresAllReports(t *testing.T) {
	env := newTestEnv(t)
	lot := env.mustRegisterLot(t, "L-ANA", "304")
	samples := env.mustSampled(t, lot.ID, "P-ANA", 2)
	rep := &domain.SpectrumReport{ReportNo: "R-ANA-1", SampleID: samples[0].ID, Readings: inRangeReadings(), Analyzer: "tester"}
	if _, err := env.daily.SubmitSpectrumReport(env.ctx, rep, "tester"); err != nil {
		t.Fatal(err)
	}
	_, err := env.daily.AnalyzeLot(env.ctx, lot.ID, 0, "tester")
	de := domain.AsDomain(err)
	if de.Code != domain.ErrCodeRuleViolation || de.Rule != domain.RuleSampleCountComplete {
		t.Fatalf("期望 R06 规则违反, 实际 %v", err)
	}
}

// TestAnalyzeLot_SharedHeatNo 共用炉批号的两批各自取样分析互不干扰。
// 回归：AnalyzeLot 曾按炉批号聚合初检光谱报告，同炉批号的另一批报告被
// 计入本批数量校验，提示“报告数量不完整”。每批应只消费自己的样本与报告。
func TestAnalyzeLot_SharedHeatNo(t *testing.T) {
	env := newTestEnv(t)

	// 两批来料共用同一炉批号 H-SHARED，各自取样 1 个样本并提交光谱报告。
	lotA := &domain.MaterialLot{
		LotNo: "L-SHARE-A", SupplierID: env.supplier.ID, HeatNo: "H-SHARED", Grade: "304",
		Quantity: 10, ReceivedAt: time.Now().UTC(),
	}
	if created, err := env.daily.RegisterLot(env.ctx, lotA, "tester"); err != nil || !created {
		t.Fatalf("登记批次A失败: created=%v err=%v", created, err)
	}
	lotB := &domain.MaterialLot{
		LotNo: "L-SHARE-B", SupplierID: env.supplier.ID, HeatNo: "H-SHARED", Grade: "304",
		Quantity: 10, ReceivedAt: time.Now().UTC(),
	}
	if created, err := env.daily.RegisterLot(env.ctx, lotB, "tester"); err != nil || !created {
		t.Fatalf("登记批次B失败: created=%v err=%v", created, err)
	}

	samplesA := env.mustSampled(t, lotA.ID, "P-SHARE-A", 1)
	samplesB := env.mustSampled(t, lotB.ID, "P-SHARE-B", 1)

	repA := &domain.SpectrumReport{ReportNo: "R-SHARE-A-1", SampleID: samplesA[0].ID, Readings: inRangeReadings(), Analyzer: "tester"}
	if _, err := env.daily.SubmitSpectrumReport(env.ctx, repA, "tester"); err != nil {
		t.Fatalf("提交批次A光谱失败: %v", err)
	}
	repB := &domain.SpectrumReport{ReportNo: "R-SHARE-B-1", SampleID: samplesB[0].ID, Readings: inRangeReadings(), Analyzer: "tester"}
	if _, err := env.daily.SubmitSpectrumReport(env.ctx, repB, "tester"); err != nil {
		t.Fatalf("提交批次B光谱失败: %v", err)
	}

	// 各自完成分析：只消费本批的报告，不受同炉批号另一批干扰。
	if _, err := env.daily.AnalyzeLot(env.ctx, lotA.ID, 0, "tester"); err != nil {
		t.Fatalf("批次A完成分析失败（同炉批号跨批干扰）: %v", err)
	}
	if _, err := env.daily.AnalyzeLot(env.ctx, lotB.ID, 0, "tester"); err != nil {
		t.Fatalf("批次B完成分析失败（同炉批号跨批干扰）: %v", err)
	}

	lotA = env.getLot(t, lotA.ID)
	lotB = env.getLot(t, lotB.ID)
	if lotA.Status != domain.LotStatusAnalyzed {
		t.Fatalf("批次A应为 analyzed: %s", lotA.Status)
	}
	if lotB.Status != domain.LotStatusAnalyzed {
		t.Fatalf("批次B应为 analyzed: %s", lotB.Status)
	}
}

// TestSubmitSpectrumReport_Idempotent 同报告编号重复提交返回 created=false。
func TestSubmitSpectrumReport_Idempotent(t *testing.T) {
	env := newTestEnv(t)
	lot := env.mustRegisterLot(t, "L-SR", "304")
	samples := env.mustSampled(t, lot.ID, "P-SR", 1)

	rep := &domain.SpectrumReport{ReportNo: "R-SR", SampleID: samples[0].ID, Readings: inRangeReadings(), Analyzer: "tester"}
	created, err := env.daily.SubmitSpectrumReport(env.ctx, rep, "tester")
	if err != nil || !created {
		t.Fatalf("首次提交失败: created=%v err=%v", created, err)
	}
	if rep.Conclusion != domain.SpectrumInRange || len(rep.Violations) != 0 {
		t.Fatalf("报告结论不符: %+v", rep)
	}

	replay := &domain.SpectrumReport{ReportNo: "R-SR", SampleID: samples[0].ID, Readings: inRangeReadings(), Analyzer: "tester"}
	created, err = env.daily.SubmitSpectrumReport(env.ctx, replay, "tester")
	if err != nil || created {
		t.Fatalf("重放应 created=false: created=%v err=%v", created, err)
	}
	if replay.ID != rep.ID {
		t.Fatalf("重放应返回既有报告: %+v", replay)
	}
}

// TestJudgeLot_R04_NoCert 无材质证明不得判定。
func TestJudgeLot_R04_NoCert(t *testing.T) {
	env := newTestEnv(t)
	lot := env.mustRegisterLot(t, "L-NOCERT", "304")
	samples := env.mustSampled(t, lot.ID, "P-NOCERT", 1)
	env.mustAnalyzed(t, lot.ID, samples, "R-NOCERT", true)

	_, err := env.daily.JudgeLot(env.ctx, lot.ID, 0, "judge1", "")
	de := domain.AsDomain(err)
	if de.Code != domain.ErrCodeRuleViolation || de.Rule != domain.RuleCertRequiredForJudgment {
		t.Fatalf("期望 R04 规则违反, 实际 %v", err)
	}
}

// TestJudgeLot_R04_UnverifiedCert 证明未核对通过不得判定。
func TestJudgeLot_R04_UnverifiedCert(t *testing.T) {
	env := newTestEnv(t)
	lot := env.mustRegisterLot(t, "L-UNVER", "304")
	samples := env.mustSampled(t, lot.ID, "P-UNVER", 1)
	env.mustAnalyzed(t, lot.ID, samples, "R-UNVER", true)

	cert := &domain.MillCertificate{CertNo: "C-UNVER", LotID: lot.ID, Grade: "304", HeatNo: lot.HeatNo, IssuedAt: time.Now().UTC()}
	if _, err := env.daily.RegisterCertificate(env.ctx, cert, "tester"); err != nil {
		t.Fatal(err)
	}
	_, err := env.daily.JudgeLot(env.ctx, lot.ID, 0, "judge1", "")
	de := domain.AsDomain(err)
	if de.Code != domain.ErrCodeRuleViolation || de.Rule != domain.RuleCertRequiredForJudgment {
		t.Fatalf("期望 R04 规则违反, 实际 %v", err)
	}
}

// TestJudgeLot_FailureRollback 判定失败后批次仍 analyzed、无结论落库。
func TestJudgeLot_FailureRollback(t *testing.T) {
	env := newTestEnv(t)
	lot := env.mustRegisterLot(t, "L-JRB", "304")
	samples := env.mustSampled(t, lot.ID, "P-JRB", 1)
	env.mustAnalyzed(t, lot.ID, samples, "R-JRB", true)

	if _, err := env.daily.JudgeLot(env.ctx, lot.ID, 0, "judge1", ""); err == nil {
		t.Fatal("无证明判定应失败")
	}
	lot = env.getLot(t, lot.ID)
	if lot.Status != domain.LotStatusAnalyzed || lot.InitialResult != "" {
		t.Fatalf("判定失败后批次应保持 analyzed 且无结论: %+v", lot)
	}
	conclusions, err := env.daily.ListConclusions(env.ctx, lot.ID)
	if err != nil || len(conclusions) != 0 {
		t.Fatalf("不应有结论落库: %v %v", conclusions, err)
	}
}

// TestJudgeLot_R05 光谱超范围判定结果为 fail。
func TestJudgeLot_R05_FailOnOutOfRange(t *testing.T) {
	env := newTestEnv(t)
	lot, _, conclusion := env.mustJudgedLot(t, "L-R05", false)
	if conclusion.Result != domain.ResultFail || conclusion.SpectrumOK {
		t.Fatalf("期望 fail 结论: %+v", conclusion)
	}
	if lot.Status != domain.LotStatusJudged || lot.InitialResult != "fail" {
		t.Fatalf("fail 后批次状态不符: %+v", lot)
	}
}

// TestVerifyCertificate_R03 证明牌号/炉批号不一致核对失败。
func TestVerifyCertificate_R03(t *testing.T) {
	env := newTestEnv(t)
	lot := env.mustRegisterLot(t, "L-R03", "304")

	// 牌号不一致
	bad := &domain.MillCertificate{CertNo: "C-R03-A", LotID: lot.ID, Grade: "316", HeatNo: lot.HeatNo, IssuedAt: time.Now().UTC()}
	if _, err := env.daily.RegisterCertificate(env.ctx, bad, "tester"); err != nil {
		t.Fatal(err)
	}
	_, err := env.daily.VerifyCertificate(env.ctx, bad.ID, 0, "tester")
	de := domain.AsDomain(err)
	if de.Code != domain.ErrCodeRuleViolation || de.Rule != domain.RuleCertMatchesLot {
		t.Fatalf("期望 R03 规则违反, 实际 %v", err)
	}

	// 炉批号不一致
	bad2 := &domain.MillCertificate{CertNo: "C-R03-B", LotID: lot.ID, Grade: "304", HeatNo: "OTHER", IssuedAt: time.Now().UTC()}
	if _, err := env.daily.RegisterCertificate(env.ctx, bad2, "tester"); err != nil {
		t.Fatal(err)
	}
	_, err = env.daily.VerifyCertificate(env.ctx, bad2.ID, 0, "tester")
	de = domain.AsDomain(err)
	if de.Code != domain.ErrCodeRuleViolation || de.Rule != domain.RuleCertMatchesLot {
		t.Fatalf("期望 R03 规则违反, 实际 %v", err)
	}
}

// TestVerifyCertificate_Idempotent 已核对的证明重复核对幂等返回。
func TestVerifyCertificate_Idempotent(t *testing.T) {
	env := newTestEnv(t)
	lot := env.mustRegisterLot(t, "L-VI", "304")
	cert := env.mustVerifiedCert(t, lot, "C-VI")
	again, err := env.daily.VerifyCertificate(env.ctx, cert.ID, 0, "other")
	if err != nil {
		t.Fatal(err)
	}
	if !again.Verified || again.VerifiedBy != "tester" {
		t.Fatalf("重复核对不应改变核对人: %+v", again)
	}
}

// TestAcceptLot_R12 不符合批次不可接收。
func TestAcceptLot_R12(t *testing.T) {
	env := newTestEnv(t)
	lot, _, _ := env.mustJudgedLot(t, "L-R12", false)
	_, err := env.daily.AcceptLot(env.ctx, lot.ID, 0, "receiver")
	de := domain.AsDomain(err)
	if de.Code != domain.ErrCodeRuleViolation || de.Rule != domain.RuleAcceptRequiresPassOrConcession {
		t.Fatalf("期望 R12 规则违反, 实际 %v", err)
	}
}

// TestAcceptLot_InvalidTransition 已接收批次（终态）重复接收走状态机校验。
func TestAcceptLot_InvalidTransition(t *testing.T) {
	env := newTestEnv(t)
	lot, _, _ := env.mustJudgedLot(t, "L-BADACC", true)
	if _, err := env.daily.AcceptLot(env.ctx, lot.ID, 0, "receiver"); err != nil {
		t.Fatal(err)
	}
	_, err := env.daily.AcceptLot(env.ctx, lot.ID, 0, "receiver")
	if !domain.IsCode(err, domain.ErrCodeInvalidTransition) {
		t.Fatalf("期望 invalid_transition, 实际 %v", err)
	}
}

// TestRejectLot 拒收仅允许 fail 批次。
func TestRejectLot(t *testing.T) {
	env := newTestEnv(t)
	passLot, _, _ := env.mustJudgedLot(t, "L-REJP", true)
	if _, err := env.daily.RejectLot(env.ctx, passLot.ID, 0, "tester", "x"); !domain.IsCode(err, domain.ErrCodeRuleViolation) {
		t.Fatalf("pass 批次拒收应违反规则, 实际 %v", err)
	}
	failLot, _, _ := env.mustJudgedLot(t, "L-REJF", false)
	rejected, err := env.daily.RejectLot(env.ctx, failLot.ID, 0, "tester", "不合格")
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Status != domain.LotStatusRejected {
		t.Fatalf("拒收后状态不符: %s", rejected.Status)
	}
}

// TestActivateGradeRule_VersionConflict 激活版本号错误返回 version_conflict。
func TestActivateGradeRule_VersionConflict(t *testing.T) {
	env := newTestEnv(t)
	rule := &domain.GradeRule{Grade: "316", VersionNo: 1, Elements: testElements}
	if _, err := env.daily.CreateGradeRule(env.ctx, rule, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.daily.ActivateGradeRule(env.ctx, rule.ID, rule.Version+1, "tester"); !domain.IsCode(err, domain.ErrCodeVersionConflict) {
		t.Fatalf("期望 version_conflict, 实际 %v", err)
	}
	// retired 不能激活
	if _, err := env.daily.RetireGradeRule(env.ctx, rule.ID, 0, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.daily.ActivateGradeRule(env.ctx, rule.ID, 0, "tester"); !domain.IsCode(err, domain.ErrCodeInvalidTransition) {
		t.Fatalf("retired 激活应 invalid_transition, 实际 %v", err)
	}
}

// TestServiceReopenPersistence 关闭重开后服务层数据仍在。
func TestServiceReopenPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")

	db, err := repository.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	daily := NewDailyService(store)
	sup := &domain.Supplier{Code: "S1", Name: "供方"}
	if _, err := daily.RegisterSupplier(ctx, sup); err != nil {
		t.Fatal(err)
	}
	rule := &domain.GradeRule{Grade: "304", VersionNo: 1, Elements: testElements}
	if _, err := daily.CreateGradeRule(ctx, rule, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := daily.ActivateGradeRule(ctx, rule.ID, 0, "tester"); err != nil {
		t.Fatal(err)
	}
	lot := &domain.MaterialLot{LotNo: "L-PERSIST", SupplierID: sup.ID, HeatNo: "H1", Grade: "304", Quantity: 1, ReceivedAt: time.Now().UTC()}
	if _, err := daily.RegisterLot(ctx, lot, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db2, err := repository.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	daily2 := NewDailyService(NewStore(db2))
	got, err := daily2.GetLot(ctx, lot.ID)
	if err != nil || got == nil || got.LotNo != "L-PERSIST" {
		t.Fatalf("重开后批次数据丢失: %v %v", got, err)
	}
	rules, err := daily2.ListGradeRules(ctx, domain.GradeRuleFilter{Grade: "304", Status: domain.RuleStatusActive},
		domain.PageRequest{Page: 1, PageSize: 10, Sort: "id", Order: "asc"})
	if err != nil || rules.Total != 1 {
		t.Fatalf("重开后规则数据丢失: %+v %v", rules, err)
	}
}
