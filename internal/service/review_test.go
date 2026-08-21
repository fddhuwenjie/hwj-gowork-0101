package service

import (
	"testing"

	"metalmics/internal/domain"
)

// TestRetestFlow_Full 覆盖复验全流程：
// fail→申请→批准→复验光谱报告→conclude 覆盖→judged→accept→ListRetestAccepted 出现该行。
func TestRetestFlow_Full(t *testing.T) {
	env := newTestEnv(t)
	lot, samples, initial := env.mustJudgedLot(t, "L-RT", false)
	retained := samples[0] // helpers 中首个样本留样

	// 申请复验
	task := &domain.RetestTask{LotID: lot.ID, SampleID: retained.ID, Reason: "供方异议", RequestedBy: "requester"}
	created, err := env.review.RequestRetest(env.ctx, task, "requester")
	if err != nil || !created {
		t.Fatalf("申请复验失败: created=%v err=%v", created, err)
	}
	lot = env.getLot(t, lot.ID)
	if lot.Status != domain.LotStatusRetesting {
		t.Fatalf("申请后应为 retesting: %s", lot.Status)
	}

	// 重复申请幂等返回既有任务
	dup := &domain.RetestTask{LotID: lot.ID, SampleID: retained.ID, Reason: "供方异议", RequestedBy: "requester"}
	created, err = env.review.RequestRetest(env.ctx, dup, "requester")
	if err != nil || created || dup.ID != task.ID {
		t.Fatalf("重复申请应幂等: created=%v err=%v id=%d", created, err, dup.ID)
	}

	// 批准（不能同人）
	if _, err := env.review.ApproveRetest(env.ctx, task.ID, 0, "requester"); !domain.IsCode(err, domain.ErrCodeRuleViolation) {
		t.Fatalf("同人批准应违反规则, 实际 %v", err)
	}
	task, err = env.review.ApproveRetest(env.ctx, task.ID, 0, "approver")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != domain.RetestStatusApproved || task.ApprovedBy != "approver" {
		t.Fatalf("批准后状态不符: %+v", task)
	}

	// 复验光谱报告（在留样上再次提交，范围内）
	rep := &domain.SpectrumReport{
		ReportNo: "R-RT-RETEST", SampleID: retained.ID,
		Readings: inRangeReadings(), Analyzer: "tester2",
	}
	created, err = env.daily.SubmitSpectrumReport(env.ctx, rep, "tester2")
	if err != nil || !created {
		t.Fatalf("复验报告提交失败: created=%v err=%v", created, err)
	}
	if rep.Conclusion != domain.SpectrumInRange {
		t.Fatalf("复验报告应在范围内: %+v", rep)
	}

	// 覆盖初检 fail->pass：必须共同决定且不能同人
	_, err = env.review.ConcludeRetest(env.ctx, task.ID, 0, domain.ResultPass, "judge2", "", "覆盖")
	if de := domain.AsDomain(err); de.Code != domain.ErrCodeRuleViolation || de.Rule != domain.RuleOverrideRequiresCoDecision {
		t.Fatalf("缺少共同决定人应违反 R09, 实际 %v", err)
	}
	_, err = env.review.ConcludeRetest(env.ctx, task.ID, 0, domain.ResultPass, "judge2", "judge2", "覆盖")
	if de := domain.AsDomain(err); de.Code != domain.ErrCodeRuleViolation || de.Rule != domain.RuleOverrideRequiresCoDecision {
		t.Fatalf("同人共同决定应违反 R09, 实际 %v", err)
	}
	// 失败回滚后任务仍可 conclude
	conclusion, err := env.review.ConcludeRetest(env.ctx, task.ID, 0, domain.ResultPass, "judge2", "co-judge", "共同决定覆盖")
	if err != nil {
		t.Fatal(err)
	}
	if conclusion.Result != domain.ResultPass || !conclusion.OverridesPrev || conclusion.CoDecidedBy != "co-judge" {
		t.Fatalf("复验结论不符: %+v", conclusion)
	}
	lot = env.getLot(t, lot.ID)
	if lot.Status != domain.LotStatusJudged || lot.RetestResult != "pass" || lot.FinalResult() != "pass" {
		t.Fatalf("复验结论落库不符: %+v", lot)
	}
	// 初检结论仍保留
	initialCheck, err := env.daily.ListConclusions(env.ctx, lot.ID)
	if err != nil || len(initialCheck) != 2 {
		t.Fatalf("应保留两轮结论: %v %v", initialCheck, err)
	}
	_ = initial

	// 接收
	if _, err := env.daily.AcceptLot(env.ctx, lot.ID, 0, "receiver"); err != nil {
		t.Fatalf("接收失败: %v", err)
	}

	// 派生查询出现该行
	page, err := env.report.ListRetestAccepted(env.ctx, domain.PageRequest{Page: 1, PageSize: 10, Sort: "id", Order: "asc"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 {
		t.Fatalf("ListRetestAccepted 应有 1 行: %+v", page)
	}
	row := page.Items[0]
	if row.LotNo != "L-RT" || row.CertNo != "C-L-RT" || row.DecidedBy != "judge2" || row.CoDecidedBy != "co-judge" {
		t.Fatalf("派生查询行内容不符: %+v", row)
	}
}

// TestRequestRetest_R07 未判定批次不能发起复验。
func TestRequestRetest_R07(t *testing.T) {
	env := newTestEnv(t)
	lot := env.mustRegisterLot(t, "L-R07", "304")
	samples := env.mustSampled(t, lot.ID, "P-R07", 1)

	task := &domain.RetestTask{LotID: lot.ID, SampleID: samples[0].ID, Reason: "x", RequestedBy: "requester"}
	_, err := env.review.RequestRetest(env.ctx, task, "requester")
	if de := domain.AsDomain(err); de.Code != domain.ErrCodeRuleViolation || de.Rule != domain.RuleRetestOnlyAfterJudgment {
		t.Fatalf("期望 R07 规则违反, 实际 %v", err)
	}
}

// TestRequestRetest_R08 非留样不能复验。
func TestRequestRetest_R08(t *testing.T) {
	env := newTestEnv(t)
	lot, samples, _ := env.mustJudgedLot(t, "L-R08", false)
	nonRetained := samples[1] // 第二个样本未留样

	task := &domain.RetestTask{LotID: lot.ID, SampleID: nonRetained.ID, Reason: "x", RequestedBy: "requester"}
	_, err := env.review.RequestRetest(env.ctx, task, "requester")
	if de := domain.AsDomain(err); de.Code != domain.ErrCodeRuleViolation || de.Rule != domain.RuleOriginalSampleRetained {
		t.Fatalf("期望 R08 规则违反, 实际 %v", err)
	}
}

// TestRetestFlow_FromRejected 拒收批次异议获准后重新进入复验。
func TestRetestFlow_FromRejected(t *testing.T) {
	env := newTestEnv(t)
	lot, samples, _ := env.mustJudgedLot(t, "L-RTRJ", false)
	if _, err := env.daily.RejectLot(env.ctx, lot.ID, 0, "tester", "不合格"); err != nil {
		t.Fatal(err)
	}

	task := &domain.RetestTask{LotID: lot.ID, SampleID: samples[0].ID, Reason: "异议", RequestedBy: "requester"}
	if _, err := env.review.RequestRetest(env.ctx, task, "requester"); err != nil {
		t.Fatal(err)
	}
	// 拒收批次申请时不改变状态，批准时才进入 retesting
	lot = env.getLot(t, lot.ID)
	if lot.Status != domain.LotStatusRejected {
		t.Fatalf("申请后应保持 rejected: %s", lot.Status)
	}
	if _, err := env.review.ApproveRetest(env.ctx, task.ID, 0, "approver"); err != nil {
		t.Fatal(err)
	}
	lot = env.getLot(t, lot.ID)
	if lot.Status != domain.LotStatusRetesting {
		t.Fatalf("批准后应为 retesting: %s", lot.Status)
	}
}

// TestConcludeRetest_MustMatchSpectrum 复验结论必须与留样光谱报告一致。
func TestConcludeRetest_MustMatchSpectrum(t *testing.T) {
	env := newTestEnv(t)
	lot, samples, _ := env.mustJudgedLot(t, "L-RTM", false)
	task := &domain.RetestTask{LotID: lot.ID, SampleID: samples[0].ID, Reason: "异议", RequestedBy: "requester"}
	if _, err := env.review.RequestRetest(env.ctx, task, "requester"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.review.ApproveRetest(env.ctx, task.ID, 0, "approver"); err != nil {
		t.Fatal(err)
	}
	// 复验报告仍超范围
	rep := &domain.SpectrumReport{ReportNo: "R-RTM-RETEST", SampleID: samples[0].ID, Readings: outOfRangeReadings(), Analyzer: "tester2"}
	if _, err := env.daily.SubmitSpectrumReport(env.ctx, rep, "tester2"); err != nil {
		t.Fatal(err)
	}
	// 宣称 pass 与证据矛盾
	_, err := env.review.ConcludeRetest(env.ctx, task.ID, 0, domain.ResultPass, "judge2", "co", "")
	if de := domain.AsDomain(err); de.Code != domain.ErrCodeRuleViolation || de.Rule != domain.RuleSpectrumWithinGradeRange {
		t.Fatalf("结论与证据不一致应违反 R05, 实际 %v", err)
	}
	// fail 与初检一致，非覆盖，无需共同决定人
	conclusion, err := env.review.ConcludeRetest(env.ctx, task.ID, 0, domain.ResultFail, "judge2", "", "维持原判")
	if err != nil {
		t.Fatal(err)
	}
	if conclusion.OverridesPrev {
		t.Fatalf("维持原判不应标记覆盖: %+v", conclusion)
	}
}

// TestConcludeRetest_OnlyRetainedSampleReportPass 复验结论只依据申请所指向留样的报告，
// 不受同批次其他样本的报告影响：留样复验合格时即便他样存在更新的不合格报告，
// 复验结论 pass 也应被接受（覆盖初检 fail）。
func TestConcludeRetest_OnlyRetainedSampleReportPass(t *testing.T) {
	env := newTestEnv(t)
	lot, samples, _ := env.mustJudgedLot(t, "L-RTSO", false) // 初检 fail
	retained := samples[0]                                    // 留样，初检超范围

	task := &domain.RetestTask{LotID: lot.ID, SampleID: retained.ID, Reason: "异议", RequestedBy: "requester"}
	if _, err := env.review.RequestRetest(env.ctx, task, "requester"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.review.ApproveRetest(env.ctx, task.ID, 0, "approver"); err != nil {
		t.Fatal(err)
	}

	// 留样复验报告：范围内（pass）
	rep := &domain.SpectrumReport{
		ReportNo: "R-RTSO-RETEST", SampleID: retained.ID,
		Readings: inRangeReadings(), Analyzer: "tester2",
	}
	if _, err := env.daily.SubmitSpectrumReport(env.ctx, rep, "tester2"); err != nil {
		t.Fatal(err)
	}

	// 同批次另一样本存在一份更新的不合格报告：留样本身的复验报告为 pass，
	// 结论不应被该样本报告否决（旧实现取整批最新报告会误判为 fail）。
	env.insertRawSpectrumReport(t, samples[1].ID, "R-RTSO-OTHER", false, rep.RuleID)

	// 结论 pass 与留样复验报告一致，可覆盖初检 fail（需共同决定人）
	conclusion, err := env.review.ConcludeRetest(env.ctx, task.ID, 0, domain.ResultPass, "judge2", "co-judge", "留样复验合格")
	if err != nil {
		t.Fatalf("留样复验合格却仍被按他样旧结果拒绝: %v", err)
	}
	if conclusion.Result != domain.ResultPass || !conclusion.OverridesPrev || !conclusion.SpectrumOK {
		t.Fatalf("复验结论应为 pass 且覆盖初检: %+v", conclusion)
	}
	lot = env.getLot(t, lot.ID)
	if lot.Status != domain.LotStatusJudged || lot.RetestResult != "pass" || lot.FinalResult() != "pass" {
		t.Fatalf("复验结论落库不符: %+v", lot)
	}
}

// TestConcludeRetest_RetainedSampleFail_StaysFail 留样复验仍超范围时，
// 复验结论必须为 fail（维持初检不合格），即便同批次其他样本存在更新的合格报告，
// 也不得以他样报告放行 pass（旧实现会误取整批最新报告而放行）。
func TestConcludeRetest_RetainedSampleFail_StaysFail(t *testing.T) {
	env := newTestEnv(t)
	lot, samples, _ := env.mustJudgedLot(t, "L-RTSF", false) // 初检 fail
	retained := samples[0]                                    // 留样，初检超范围

	task := &domain.RetestTask{LotID: lot.ID, SampleID: retained.ID, Reason: "异议", RequestedBy: "requester"}
	if _, err := env.review.RequestRetest(env.ctx, task, "requester"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.review.ApproveRetest(env.ctx, task.ID, 0, "approver"); err != nil {
		t.Fatal(err)
	}

	// 留样复验报告仍超范围（fail）
	rep := &domain.SpectrumReport{
		ReportNo: "R-RTSF-RETEST", SampleID: retained.ID,
		Readings: outOfRangeReadings(), Analyzer: "tester2",
	}
	if _, err := env.daily.SubmitSpectrumReport(env.ctx, rep, "tester2"); err != nil {
		t.Fatal(err)
	}

	// 同批次另一样本存在一份更新的合格报告，不得据此放行 pass
	env.insertRawSpectrumReport(t, samples[1].ID, "R-RTSF-OTHER", true, rep.RuleID)

	// 宣称 pass 与留样证据矛盾 → 违反 R05
	_, err := env.review.ConcludeRetest(env.ctx, task.ID, 0, domain.ResultPass, "judge2", "co", "")
	if err == nil {
		t.Fatalf("留样仍超范围，宣称 pass 应被拒绝，却被放行")
	}
	if de := domain.AsDomain(err); de.Code != domain.ErrCodeRuleViolation || de.Rule != domain.RuleSpectrumWithinGradeRange {
		t.Fatalf("留样仍超范围却宣称 pass 应违反 R05, 实际 %v", err)
	}

	// fail 与初检一致，非覆盖，无需共同决定人 → 复验以不合格结束
	conclusion, err := env.review.ConcludeRetest(env.ctx, task.ID, 0, domain.ResultFail, "judge2", "", "维持原判")
	if err != nil {
		t.Fatalf("留样仍超范围，复验应维持 fail: %v", err)
	}
	if conclusion.Result != domain.ResultFail || conclusion.OverridesPrev {
		t.Fatalf("复验结论应为 fail 且不覆盖: %+v", conclusion)
	}
	lot = env.getLot(t, lot.ID)
	if lot.Status != domain.LotStatusJudged || lot.RetestResult != "fail" || lot.FinalResult() != "fail" {
		t.Fatalf("复验不合格后批次应维持 judged/fail: %+v", lot)
	}
}

// TestRejectRetest 驳回复验申请后批次回到 judged。
func TestRejectRetest(t *testing.T) {
	env := newTestEnv(t)
	lot, samples, _ := env.mustJudgedLot(t, "L-RTR", false)
	task := &domain.RetestTask{LotID: lot.ID, SampleID: samples[0].ID, Reason: "异议", RequestedBy: "requester"}
	if _, err := env.review.RequestRetest(env.ctx, task, "requester"); err != nil {
		t.Fatal(err)
	}
	task, err := env.review.RejectRetest(env.ctx, task.ID, 0, "approver")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != domain.RetestStatusRejected {
		t.Fatalf("驳回后状态不符: %+v", task)
	}
	lot = env.getLot(t, lot.ID)
	if lot.Status != domain.LotStatusJudged || lot.InitialResult != "fail" {
		t.Fatalf("驳回后批次应回到 judged: %+v", lot)
	}
}
