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

// TestAcceptLot_RetestPassOverridesInitialFailWithConcession 复现状态链路：
// 初检 fail → 让步审批通过 → 复验合格（共同决定覆盖）→ 接收。
// 复验结论已覆盖初检后，接收应按当前结论（pass）放行，不得再被历史初检 fail 拦住。
func TestAcceptLot_RetestPassOverridesInitialFailWithConcession(t *testing.T) {
	env := newTestEnv(t)
	lot, samples, _ := env.mustJudgedLot(t, "L-RTCONC", false)
	retained := samples[0] // helpers 中首个样本留样

	// 让步接收：提出 → 批准（不改变批次状态，仅赋予接收资格）
	disp := &domain.Disposition{LotID: lot.ID, Type: domain.DispositionConcession, Reason: "客户同意让步", ProposedBy: "proposer"}
	if _, err := env.exception.ProposeDisposition(env.ctx, disp, "proposer"); err != nil {
		t.Fatalf("提出让步失败: %v", err)
	}
	if _, err := env.exception.ApproveDisposition(env.ctx, disp.ID, 0, "approver"); err != nil {
		t.Fatalf("批准让步失败: %v", err)
	}

	// 异议复验：申请 → 批准 → 复验光谱报告（范围内）→ 结论 pass（覆盖初检，须共同决定）
	task := &domain.RetestTask{LotID: lot.ID, SampleID: retained.ID, Reason: "供方异议", RequestedBy: "requester"}
	if _, err := env.review.RequestRetest(env.ctx, task, "requester"); err != nil {
		t.Fatalf("申请复验失败: %v", err)
	}
	if _, err := env.review.ApproveRetest(env.ctx, task.ID, 0, "approver"); err != nil {
		t.Fatalf("批准复验失败: %v", err)
	}
	rep := &domain.SpectrumReport{ReportNo: "R-RTCONC-RETEST", SampleID: retained.ID, Readings: inRangeReadings(), Analyzer: "tester2"}
	if _, err := env.daily.SubmitSpectrumReport(env.ctx, rep, "tester2"); err != nil {
		t.Fatalf("复验报告提交失败: %v", err)
	}
	conclusion, err := env.review.ConcludeRetest(env.ctx, task.ID, 0, domain.ResultPass, "judge2", "co-judge", "共同决定覆盖")
	if err != nil {
		t.Fatalf("出具复验结论失败: %v", err)
	}
	if !conclusion.OverridesPrev || conclusion.CoDecidedBy != "co-judge" {
		t.Fatalf("复验结论未标记覆盖/缺少共同决定人: %+v", conclusion)
	}
	lot = env.getLot(t, lot.ID)
	if lot.InitialResult != "fail" || lot.RetestResult != "pass" || lot.FinalResult() != "pass" {
		t.Fatalf("结论落库不符: initial=%s retest=%s final=%s", lot.InitialResult, lot.RetestResult, lot.FinalResult())
	}

	// 接收：复验已覆盖初检，应按当前结论 pass 放行，不被历史初检 fail 拦住
	accepted, err := env.daily.AcceptLot(env.ctx, lot.ID, 0, "receiver")
	if err != nil {
		t.Fatalf("复验覆盖初检后接收应成功，却被拦住: %v", err)
	}
	if accepted.Status != domain.LotStatusAccepted || accepted.AcceptedBy != "receiver" || accepted.AcceptedAt == nil {
		t.Fatalf("接收结果不符: %+v", accepted)
	}
}

// TestAcceptLot_NoRetestPassEvidenceNotDirectlyReceived 保留约束：
// 没有复验通过证据（FinalResult 非 pass）的批次不得直接接收，须凭让步单。
func TestAcceptLot_NoRetestPassEvidenceNotDirectlyReceived(t *testing.T) {
	env := newTestEnv(t)
	lot, samples, _ := env.mustJudgedLot(t, "L-NOPASS", false)
	retained := samples[0]

	// 复验复跑后仍 fail：无复验通过证据，FinalResult 仍为 fail
	task := &domain.RetestTask{LotID: lot.ID, SampleID: retained.ID, Reason: "异议", RequestedBy: "requester"}
	if _, err := env.review.RequestRetest(env.ctx, task, "requester"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.review.ApproveRetest(env.ctx, task.ID, 0, "approver"); err != nil {
		t.Fatal(err)
	}
	rep := &domain.SpectrumReport{ReportNo: "R-NOPASS-RETEST", SampleID: retained.ID, Readings: outOfRangeReadings(), Analyzer: "tester2"}
	if _, err := env.daily.SubmitSpectrumReport(env.ctx, rep, "tester2"); err != nil {
		t.Fatal(err)
	}
	// fail 与初检一致，非覆盖，无需共同决定人
	if _, err := env.review.ConcludeRetest(env.ctx, task.ID, 0, domain.ResultFail, "judge2", "", "维持原判"); err != nil {
		t.Fatal(err)
	}
	lot = env.getLot(t, lot.ID)
	if lot.RetestResult != "fail" || lot.FinalResult() != "fail" {
		t.Fatalf("复验 fail 后结论不符: retest=%s final=%s", lot.RetestResult, lot.FinalResult())
	}

	// 无让步：无复验通过证据，不得直接接收（R12）
	if _, err := env.daily.AcceptLot(env.ctx, lot.ID, 0, "receiver"); err == nil {
		t.Fatal("无复验通过证据且无让步的批次不得直接接收")
	} else if de := domain.AsDomain(err); de.Code != domain.ErrCodeRuleViolation || de.Rule != domain.RuleAcceptRequiresPassOrConcession {
		t.Fatalf("期望 R12 规则违反, 实际 %v", err)
	}

	// 有让步：凭让步单可接收（非直接接收）
	disp := &domain.Disposition{LotID: lot.ID, Type: domain.DispositionConcession, Reason: "让步", ProposedBy: "proposer"}
	if _, err := env.exception.ProposeDisposition(env.ctx, disp, "proposer"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.exception.ApproveDisposition(env.ctx, disp.ID, 0, "approver"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.daily.AcceptLot(env.ctx, lot.ID, 0, "receiver"); err != nil {
		t.Fatalf("凭已批准让步单的 fail 批次应可接收: %v", err)
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
