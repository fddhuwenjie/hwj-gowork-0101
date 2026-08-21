package service

import (
	"testing"

	"metalmics/internal/domain"
)

// TestCreateSamplingPlan_OnlyRegisteredLot 取样计划只能绑定仍处于登记阶段的批次，
// 且每批次仅一份计划；对已进入后续取样状态的批次重复提交（同号或异号）均不得被接受，
// 也不得引发 panic / 500。
func TestCreateSamplingPlan_OnlyRegisteredLot(t *testing.T) {
	env := newTestEnv(t)
	lot := env.mustRegisterLot(t, "L-REG", "304")

	// registered 阶段：首次建计划成功
	plan := &domain.SamplingPlan{PlanNo: "P-REG", LotID: lot.ID, RequiredCount: 1, RetainLocation: "柜A", CreatedBy: "tester"}
	created, err := env.daily.CreateSamplingPlan(env.ctx, plan, "tester")
	if err != nil || !created {
		t.Fatalf("registered 阶段建计划应成功: created=%v err=%v", created, err)
	}

	// registered 阶段：同 plan_no 重放幂等，created=false 且不报错
	replay := &domain.SamplingPlan{PlanNo: "P-REG", LotID: lot.ID, RequiredCount: 1, RetainLocation: "柜A", CreatedBy: "tester"}
	created, err = env.daily.CreateSamplingPlan(env.ctx, replay, "tester")
	if err != nil || created {
		t.Fatalf("同号重放应幂等 created=false: created=%v err=%v", created, err)
	}
	if replay.ID != plan.ID {
		t.Fatalf("重放应返回既有计划: got=%+v want id=%d", replay, plan.ID)
	}

	// registered 阶段：不同 plan_no 但同 lot_id（已有计划），按版本冲突拒绝
	other := &domain.SamplingPlan{PlanNo: "P-REG-2", LotID: lot.ID, RequiredCount: 2, RetainLocation: "柜B", CreatedBy: "tester"}
	_, err = env.daily.CreateSamplingPlan(env.ctx, other, "tester")
	if !domain.IsCode(err, domain.ErrCodeVersionConflict) {
		t.Fatalf("已绑定计划的批次异号提交应 version_conflict, 实际 %v", err)
	}

	// 推进到 sampled 后再次提交：同号重放幂等、异号提交被拒
	smp := &domain.Sample{SampleNo: "S-REG-1", Retained: true}
	if _, err := env.daily.RegisterSamples(env.ctx, plan.ID, []*domain.Sample{smp}, "tester"); err != nil {
		t.Fatalf("登记样本失败: %v", err)
	}
	if _, err := env.daily.CompleteSampling(env.ctx, lot.ID, 0, "tester"); err != nil {
		t.Fatalf("完成取样失败: %v", err)
	}
	lot = env.getLot(t, lot.ID)
	if lot.Status != domain.LotStatusSampled {
		t.Fatalf("批次应为 sampled: %s", lot.Status)
	}

	// sampled 阶段：同 plan_no 重放仍幂等返回既有计划
	replay2 := &domain.SamplingPlan{PlanNo: "P-REG", LotID: lot.ID, RequiredCount: 1, RetainLocation: "柜A", CreatedBy: "tester"}
	created, err = env.daily.CreateSamplingPlan(env.ctx, replay2, "tester")
	if err != nil || created {
		t.Fatalf("sampled 阶段同号重放应幂等: created=%v err=%v", created, err)
	}
	if replay2.ID != plan.ID {
		t.Fatalf("sampled 阶段重放应返回既有计划: got=%+v want id=%d", replay2, plan.ID)
	}

	// sampled 阶段：不同 plan_no 的新计划必须被拒（非法状态转换），不得被接受
	diff := &domain.SamplingPlan{PlanNo: "P-REG-3", LotID: lot.ID, RequiredCount: 5, RetainLocation: "柜Z", CreatedBy: "tester"}
	_, err = env.daily.CreateSamplingPlan(env.ctx, diff, "tester")
	if !domain.IsCode(err, domain.ErrCodeInvalidTransition) {
		t.Fatalf("sampled 阶段异号新建应 invalid_transition, 实际 %v", err)
	}

	// 数据库中该批次只能有一份计划
	got, gerr := env.store.DB().Query("SELECT COUNT(*) FROM sampling_plans WHERE lot_id = ?", lot.ID)
	if gerr != nil {
		t.Fatalf("查询计划失败: %v", gerr)
	}
	defer got.Close()
	var n int
	if !got.Next() {
		t.Fatal("无结果")
	}
	if err := got.Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("该批次应只有 1 份计划, 实际 %d", n)
	}
}

// TestCreateSamplingPlan_RejectsNonRegisteredStatuses 逐一覆盖后续取样阶段，
// 确保任何离开 registered 的状态都不允许绑定新取样计划。
func TestCreateSamplingPlan_RejectsNonRegisteredStatuses(t *testing.T) {
	cases := []struct {
		name  string
		setup func(env *testEnv, t *testing.T) int64 // 返回处于目标状态的批次 id
	}{
		{
			name: "sampled",
			setup: func(env *testEnv, t *testing.T) int64 {
				lot := env.mustRegisterLot(t, "L-S", "304")
				env.mustSampled(t, lot.ID, "P-S", 1)
				return lot.ID
			},
		},
		{
			name: "analyzed",
			setup: func(env *testEnv, t *testing.T) int64 {
				lot := env.mustRegisterLot(t, "L-A", "304")
				samples := env.mustSampled(t, lot.ID, "P-A", 1)
				env.mustAnalyzed(t, lot.ID, samples, "R-A", true)
				return lot.ID
			},
		},
		{
			name: "quarantined",
			setup: func(env *testEnv, t *testing.T) int64 {
				lot := env.mustRegisterLot(t, "L-Q", "304")
				disp := &domain.Disposition{LotID: lot.ID, Type: domain.DispositionQuarantine, Reason: "异常", ProposedBy: "tester"}
				if _, err := env.exception.ProposeDisposition(env.ctx, disp, "tester"); err != nil {
					t.Fatalf("提出隔离失败: %v", err)
				}
				return lot.ID
			},
		},
		{
			name: "accepted",
			setup: func(env *testEnv, t *testing.T) int64 {
				lot, _, _ := env.mustJudgedLot(t, "L-ACC", true)
				if _, err := env.daily.AcceptLot(env.ctx, lot.ID, 0, "receiver"); err != nil {
					t.Fatalf("接收失败: %v", err)
				}
				return lot.ID
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			lotID := tc.setup(env, t)
			// 该批次已有一份（前序流程建立的）计划，用全新 plan_no 提交第二份应被拒
			plan := &domain.SamplingPlan{PlanNo: "P-EXTRA-" + tc.name, LotID: lotID, RequiredCount: 1, RetainLocation: "柜X", CreatedBy: "tester"}
			_, err := env.daily.CreateSamplingPlan(env.ctx, plan, "tester")
			if err == nil {
				t.Fatalf("%s: 非登记阶段提交新计划应被拒绝, 实际成功", tc.name)
			}
			if !domain.IsCode(err, domain.ErrCodeInvalidTransition) {
				t.Fatalf("%s: 期望 invalid_transition, 实际 %v", tc.name, err)
			}
		})
	}
}

// TestCreateSamplingPlan_UnknownLot 批次不存在时返回 not_found。
func TestCreateSamplingPlan_UnknownLot(t *testing.T) {
	env := newTestEnv(t)
	plan := &domain.SamplingPlan{PlanNo: "P-GHOST", LotID: 99999, RequiredCount: 1, RetainLocation: "柜A", CreatedBy: "tester"}
	_, err := env.daily.CreateSamplingPlan(env.ctx, plan, "tester")
	if !domain.IsCode(err, domain.ErrCodeNotFound) {
		t.Fatalf("期望 not_found, 实际 %v", err)
	}
}
