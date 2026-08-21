package service

import (
	"testing"

	"metalmics/internal/domain"
)

// TestQuarantine_UrgentRelease 隔离→批准→紧急放行→accepted，且被缺失证明统计命中。
func TestQuarantine_UrgentRelease(t *testing.T) {
	env := newTestEnv(t)
	lot := env.mustRegisterLot(t, "L-URG", "304")

	// 隔离中不得直接接收（R11）
	d := &domain.Disposition{LotID: lot.ID, Type: domain.DispositionQuarantine, Reason: "疑似混料", ProposedBy: "tester"}
	created, err := env.exception.ProposeDisposition(env.ctx, d, "tester")
	if err != nil || !created {
		t.Fatalf("提出隔离失败: created=%v err=%v", created, err)
	}
	lot = env.getLot(t, lot.ID)
	if lot.Status != domain.LotStatusQuarantined {
		t.Fatalf("提出隔离后应为 quarantined: %s", lot.Status)
	}
	if _, err := env.daily.AcceptLot(env.ctx, lot.ID, 0, "receiver"); err == nil {
		t.Fatal("隔离中不应允许直接接收")
	} else if de := domain.AsDomain(err); de.Code != domain.ErrCodeRuleViolation || de.Rule != domain.RuleQuarantineBlocksAccept {
		t.Fatalf("期望 R11 规则违反, 实际 %v", err)
	}

	// 批准（同人禁止）
	if _, err := env.exception.ApproveDisposition(env.ctx, d.ID, 0, "tester"); !domain.IsCode(err, domain.ErrCodeRuleViolation) {
		t.Fatalf("同人批准应违反规则, 实际 %v", err)
	}
	d, err = env.exception.ApproveDisposition(env.ctx, d.ID, 0, "boss")
	if err != nil {
		t.Fatal(err)
	}

	// 执行紧急放行
	d, err = env.exception.ExecuteDisposition(env.ctx, d.ID, d.Version, "boss", "urgent_release")
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != domain.DispositionExecuted || d.Resolution != "urgent_release" {
		t.Fatalf("处置单执行结果不符: %+v", d)
	}
	lot = env.getLot(t, lot.ID)
	if lot.Status != domain.LotStatusAccepted {
		t.Fatalf("紧急放行后应为 accepted: %s", lot.Status)
	}

	// 无证明先接收：被 CountCertMissingAccepted 统计到
	rows, err := env.report.CountCertMissingAccepted(env.ctx, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].SupplierID != env.supplier.ID || rows[0].LotCount != 1 {
		t.Fatalf("缺失证明统计不符: %+v", rows)
	}
}

// TestQuarantine_Scrap 隔离后报废执行使批次 rejected。
func TestQuarantine_Scrap(t *testing.T) {
	env := newTestEnv(t)
	lot := env.mustRegisterLot(t, "L-SCRAP", "304")
	d := &domain.Disposition{LotID: lot.ID, Type: domain.DispositionQuarantine, Reason: "污染", ProposedBy: "tester"}
	if _, err := env.exception.ProposeDisposition(env.ctx, d, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.exception.ApproveDisposition(env.ctx, d.ID, 0, "boss"); err != nil {
		t.Fatal(err)
	}
	d, err := env.exception.ExecuteDisposition(env.ctx, d.ID, 0, "boss", "scrap")
	if err != nil {
		t.Fatal(err)
	}
	lot = env.getLot(t, lot.ID)
	if lot.Status != domain.LotStatusRejected || d.Status != domain.DispositionExecuted {
		t.Fatalf("报废后状态不符: lot=%s disp=%s", lot.Status, d.Status)
	}
}

// TestConcession_R10 让步接收仅允许 fail 批次；批准后批次可接收。
func TestConcession_R10(t *testing.T) {
	env := newTestEnv(t)

	// pass 批次不允许让步接收
	passLot, _, _ := env.mustJudgedLot(t, "L-CONP", true)
	d := &domain.Disposition{LotID: passLot.ID, Type: domain.DispositionConcession, Reason: "让步", ProposedBy: "tester"}
	_, err := env.exception.ProposeDisposition(env.ctx, d, "tester")
	if de := domain.AsDomain(err); de.Code != domain.ErrCodeRuleViolation || de.Rule != domain.RuleConcessionRequiresFailure {
		t.Fatalf("期望 R10 规则违反, 实际 %v", err)
	}

	// fail 批次：提出→批准→接收
	failLot, _, _ := env.mustJudgedLot(t, "L-CONF", false)
	d = &domain.Disposition{LotID: failLot.ID, Type: domain.DispositionConcession, Reason: "客户同意让步", ProposedBy: "tester"}
	if _, err := env.exception.ProposeDisposition(env.ctx, d, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.exception.ApproveDisposition(env.ctx, d.ID, 0, "boss"); err != nil {
		t.Fatal(err)
	}
	accepted, err := env.daily.AcceptLot(env.ctx, failLot.ID, 0, "receiver")
	if err != nil {
		t.Fatalf("有让步接收的 fail 批次应可接收: %v", err)
	}
	if accepted.Status != domain.LotStatusAccepted {
		t.Fatalf("状态不符: %s", accepted.Status)
	}
}

// TestDisposition_RejectSeversExecution 驳回是处置单的终止态：
// 驳回返回的对象状态必须为 rejected，且后续 Execute 不得改变批次状态。
// （回归：此前驳回返回 approved，被后续流程误判为可执行。）
func TestDisposition_RejectSeversExecution(t *testing.T) {
	env := newTestEnv(t)
	lot := env.mustRegisterLot(t, "L-REJ", "304")

	d := &domain.Disposition{LotID: lot.ID, Type: domain.DispositionQuarantine, Reason: "疑似混料", ProposedBy: "tester"}
	if _, err := env.exception.ProposeDisposition(env.ctx, d, "tester"); err != nil {
		t.Fatal(err)
	}
	lot = env.getLot(t, lot.ID)
	if lot.Status != domain.LotStatusQuarantined {
		t.Fatalf("提出隔离后应为 quarantined: %s", lot.Status)
	}

	// 驳回（与提出人不同）
	rejected, err := env.exception.RejectDisposition(env.ctx, d.ID, d.Version, "auditor")
	if err != nil {
		t.Fatalf("驳回失败: %v", err)
	}
	if rejected.Status != domain.DispositionRejected {
		t.Fatalf("驳回返回状态应为 rejected, 实际 %s", rejected.Status)
	}
	if rejected.ApprovedBy != "auditor" {
		t.Fatalf("驳回返回 approved_by 应记录驳回人: %s", rejected.ApprovedBy)
	}

	// 再次执行被切断：返回非法状态转换，批次保持隔离
	if _, err := env.exception.ExecuteDisposition(env.ctx, d.ID, rejected.Version, "ops", "urgent_release"); !domain.IsCode(err, domain.ErrCodeInvalidTransition) {
		t.Fatalf("驳回后执行应被切断为 invalid_transition, 实际 %v", err)
	}
	lot = env.getLot(t, lot.ID)
	if lot.Status != domain.LotStatusQuarantined {
		t.Fatalf("驳回后批次应保持 quarantined: %s", lot.Status)
	}

	// 驳回为终态：再次驳回应非法状态转换
	if _, err := env.exception.RejectDisposition(env.ctx, d.ID, rejected.Version, "auditor"); !domain.IsCode(err, domain.ErrCodeInvalidTransition) {
		t.Fatalf("终态再驳回应 invalid_transition, 实际 %v", err)
	}
}

// TestDisposition_RejectSeversAssociation 驳回切断了处置单与批次的“未关闭”关联：
// 同批次同类型可再提一张新单（部分唯一索引只约束 proposed/approved），并落库为新记录。
func TestDisposition_RejectSeversAssociation(t *testing.T) {
	env := newTestEnv(t)
	failLot, _, _ := env.mustJudgedLot(t, "L-REJCONC2", false)

	d := &domain.Disposition{LotID: failLot.ID, Type: domain.DispositionConcession, Reason: "让步", ProposedBy: "tester"}
	if _, err := env.exception.ProposeDisposition(env.ctx, d, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.exception.RejectDisposition(env.ctx, d.ID, d.Version, "auditor"); err != nil {
		t.Fatal(err)
	}

	// 驳回后同类型（让步）可再提一张新单，并入库为新 id
	d2 := &domain.Disposition{LotID: failLot.ID, Type: domain.DispositionConcession, Reason: "再次让步", ProposedBy: "tester"}
	created, err := env.exception.ProposeDisposition(env.ctx, d2, "tester")
	if err != nil || !created {
		t.Fatalf("驳回后应可重新提出处置单: created=%v err=%v", created, err)
	}
	if d2.ID == d.ID {
		t.Fatalf("新处置单应为新记录 id=%d, 与已驳回单 id=%d 相同", d2.ID, d.ID)
	}
	// 旧驳回单不再被视为未关闭，查不到 open 项
	page, err := env.exception.ListDispositions(env.ctx, domain.DispositionFilter{LotID: failLot.ID, Status: domain.DispositionRejected},
		domain.PageRequest{Page: 1, PageSize: 10, Sort: "id", Order: "asc"})
	if err != nil || page.Total != 1 || page.Items[0].ID != d.ID {
		t.Fatalf("已驳回单应可被 rejected 过滤命中: %+v err=%v", page, err)
	}
}

// TestConcession_RejectBlocksAccept 让步接收单被驳回后不再为 fail 批次提供接收资格，
// AcceptLot 仍受 R12 拦截，批次保持 judged。
func TestConcession_RejectBlocksAccept(t *testing.T) {
	env := newTestEnv(t)
	failLot, _, _ := env.mustJudgedLot(t, "L-REJCONC", false)

	d := &domain.Disposition{LotID: failLot.ID, Type: domain.DispositionConcession, Reason: "客户撤回让步", ProposedBy: "tester"}
	if _, err := env.exception.ProposeDisposition(env.ctx, d, "tester"); err != nil {
		t.Fatal(err)
	}
	rejected, err := env.exception.RejectDisposition(env.ctx, d.ID, d.Version, "auditor")
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Status != domain.DispositionRejected {
		t.Fatalf("让步单驳回返回应为 rejected: %s", rejected.Status)
	}

	// 驳回的让步单不应使 fail 批次获得接收资格
	if _, err := env.daily.AcceptLot(env.ctx, failLot.ID, 0, "receiver"); !domain.IsCode(err, domain.ErrCodeRuleViolation) {
		t.Fatalf("让步单驳回后接收应被 R12 拦截, 实际 %v", err)
	}
	lot := env.getLot(t, failLot.ID)
	if lot.Status != domain.LotStatusJudged {
		t.Fatalf("让步单驳回后批次应保持 judged: %s", lot.Status)
	}
}

// TestBatchAccept_PartialFailure 批量接收部分失败。
func TestBatchAccept_PartialFailure(t *testing.T) {
	env := newTestEnv(t)
	ok1, _, _ := env.mustJudgedLot(t, "L-BA1", true)
	ok2, _, _ := env.mustJudgedLot(t, "L-BA2", true)
	// 已接收批次（终态）再接收 -> invalid_transition
	accepted, _, _ := env.mustJudgedLot(t, "L-BA3", true)
	if _, err := env.daily.AcceptLot(env.ctx, accepted.ID, 0, "receiver"); err != nil {
		t.Fatal(err)
	}
	failLot, _, _ := env.mustJudgedLot(t, "L-BA4", false) // fail 无让步，R12
	missingID := int64(99999)

	result, err := env.daily.BatchAccept(env.ctx, []int64{ok1.ID, accepted.ID, ok2.ID, failLot.ID, missingID}, "receiver")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Succeeded) != 2 || len(result.Failed) != 3 {
		t.Fatalf("批量接收结果不符: %+v", result)
	}
	if result.Succeeded[0].LotID != ok1.ID || result.Succeeded[1].LotID != ok2.ID {
		t.Fatalf("成功项不符: %+v", result.Succeeded)
	}
	// 失败项按输入顺序携带错误码
	if result.Failed[0].LotID != accepted.ID || result.Failed[0].Code != string(domain.ErrCodeInvalidTransition) {
		t.Fatalf("失败项[0]不符: %+v", result.Failed[0])
	}
	if result.Failed[1].LotID != failLot.ID || result.Failed[1].Code != string(domain.ErrCodeRuleViolation) {
		t.Fatalf("失败项[1]不符: %+v", result.Failed[1])
	}
	if result.Failed[2].LotID != missingID || result.Failed[2].Code != string(domain.ErrCodeNotFound) {
		t.Fatalf("失败项[2]不符: %+v", result.Failed[2])
	}

	// 空列表与超量校验
	if _, err := env.daily.BatchAccept(env.ctx, nil, "receiver"); !domain.IsCode(err, domain.ErrCodeValidation) {
		t.Fatalf("空列表应 validation, 实际 %v", err)
	}
	tooMany := make([]int64, 101)
	if _, err := env.daily.BatchAccept(env.ctx, tooMany, "receiver"); !domain.IsCode(err, domain.ErrCodeValidation) {
		t.Fatalf("超量应 validation, 实际 %v", err)
	}
}
