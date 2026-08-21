package service

import (
	"testing"
	"time"

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

// TestRetestAccepted_FollowsLatestCert 复验接收后补录新材质证明，
// 派生查询应跟随最后一次登记的证明编号，而非最早登记的旧证明。
// 回归用户反馈：审核人员在报表中看到的是旧证明编号。
func TestRetestAccepted_FollowsLatestCert(t *testing.T) {
	env := newTestEnv(t)
	lot, samples, _ := env.mustJudgedLot(t, "L-RTC", false)
	retained := samples[0]

	task := &domain.RetestTask{LotID: lot.ID, SampleID: retained.ID, Reason: "供方异议", RequestedBy: "requester"}
	if _, err := env.review.RequestRetest(env.ctx, task, "requester"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.review.ApproveRetest(env.ctx, task.ID, 0, "approver"); err != nil {
		t.Fatal(err)
	}
	rep := &domain.SpectrumReport{
		ReportNo: "R-RTC-RETEST", SampleID: retained.ID,
		Readings: inRangeReadings(), Analyzer: "tester2",
	}
	if _, err := env.daily.SubmitSpectrumReport(env.ctx, rep, "tester2"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.review.ConcludeRetest(env.ctx, task.ID, 0, domain.ResultPass, "judge2", "co-judge", "共同决定覆盖"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.daily.AcceptLot(env.ctx, lot.ID, 0, "receiver"); err != nil {
		t.Fatalf("接收失败: %v", err)
	}

	// 接收后补录新材质证明（接收为终态，证明登记应在接收之前完成；
	// 为复现“补录”语义，在接收前的 judged 状态下补录第二条证明）。
	// 这里重新走一遍流程并在接收前补录，确保报表跟随新证明。
	env2 := newTestEnv(t)
	lot2, samples2, _ := env2.mustJudgedLot(t, "L-RTC2", false)
	retained2 := samples2[0]
	task2 := &domain.RetestTask{LotID: lot2.ID, SampleID: retained2.ID, Reason: "异议", RequestedBy: "requester"}
	if _, err := env2.review.RequestRetest(env.ctx, task2, "requester"); err != nil {
		t.Fatal(err)
	}
	if _, err := env2.review.ApproveRetest(env.ctx, task2.ID, 0, "approver"); err != nil {
		t.Fatal(err)
	}
	rep2 := &domain.SpectrumReport{
		ReportNo: "R-RTC2-RETEST", SampleID: retained2.ID,
		Readings: inRangeReadings(), Analyzer: "tester2",
	}
	if _, err := env2.daily.SubmitSpectrumReport(env.ctx, rep2, "tester2"); err != nil {
		t.Fatal(err)
	}
	if _, err := env2.review.ConcludeRetest(env.ctx, task2.ID, 0, domain.ResultPass, "judge2", "co-judge", "覆盖"); err != nil {
		t.Fatal(err)
	}
	// 补录新证明：在接收前（judged 状态，非终态）登记并核对第二条证明。
	newCert := &domain.MillCertificate{
		CertNo: "C-RTC2-NEW", LotID: lot2.ID, Grade: lot2.Grade, HeatNo: lot2.HeatNo,
		IssuedAt: time.Now().UTC(),
	}
	if _, err := env2.daily.RegisterCertificate(env.ctx, newCert, "tester"); err != nil {
		t.Fatalf("补录新证明登记失败: %v", err)
	}
	if _, err := env2.daily.VerifyCertificate(env.ctx, newCert.ID, 0, "tester"); err != nil {
		t.Fatalf("补录新证明核对失败: %v", err)
	}
	if _, err := env2.daily.AcceptLot(env.ctx, lot2.ID, 0, "receiver"); err != nil {
		t.Fatalf("接收失败: %v", err)
	}

	page, err := env2.report.ListRetestAccepted(env.ctx, domain.PageRequest{Page: 1, PageSize: 10, Sort: "id", Order: "asc"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 {
		t.Fatalf("ListRetestAccepted 应有 1 行: %+v", page)
	}
	row := page.Items[0]
	if row.CertNo != "C-RTC2-NEW" {
		t.Fatalf("报表应跟随最后一次登记的新证明编号 C-RTC2-NEW, 实际 %s", row.CertNo)
	}
}

