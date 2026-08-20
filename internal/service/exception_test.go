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
