package service

import (
	"testing"
	"time"

	"metalmics/internal/domain"
	"metalmics/internal/repository"
)

// makeAcceptedWithoutCert 通过隔离+紧急放行构造一个无证明先接收的批次。
func makeAcceptedWithoutCert(t *testing.T, env *testEnv, lotNo string) *domain.MaterialLot {
	t.Helper()
	lot := env.mustRegisterLot(t, lotNo, "304")
	d := &domain.Disposition{LotID: lot.ID, Type: domain.DispositionQuarantine, Reason: "紧急", ProposedBy: "tester"}
	if _, err := env.exception.ProposeDisposition(env.ctx, d, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.exception.ApproveDisposition(env.ctx, d.ID, 0, "boss"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.exception.ExecuteDisposition(env.ctx, d.ID, 0, "boss", "urgent_release"); err != nil {
		t.Fatal(err)
	}
	return env.getLot(t, lot.ID)
}

// TestJob_CertMissingScan_Success 合法任务执行成功并写出审计事件。
func TestJob_CertMissingScan_Success(t *testing.T) {
	env := newTestEnv(t)
	lot := makeAcceptedWithoutCert(t, env, "L-JOB1")

	job, err := env.jobs.Enqueue(env.ctx, JobTypeCertMissingScan, map[string]interface{}{"days": 30}, 3)
	if err != nil {
		t.Fatal(err)
	}
	ran, err := env.jobs.RunDue(env.ctx)
	if err != nil || !ran {
		t.Fatalf("RunDue: ran=%v err=%v", ran, err)
	}
	got, err := repository.NewJobRepo(env.db).GetByID(env.ctx, job.ID)
	if err != nil || got == nil || got.Status != domain.JobStatusDone {
		t.Fatalf("任务应为 done: %+v err=%v", got, err)
	}

	// 审计告警已写入
	audits, err := env.report.ListAuditEvents(env.ctx,
		domain.AuditFilter{Entity: "material_lot", EntityID: lot.ID, Actor: "job:" + JobTypeCertMissingScan},
		domain.PageRequest{Page: 1, PageSize: 10, Sort: "id", Order: "asc"})
	if err != nil {
		t.Fatal(err)
	}
	if audits.Total != 1 || audits.Items[0].Action != "cert_missing_flagged" {
		t.Fatalf("缺失证明审计事件不符: %+v", audits)
	}

	// 无更多任务可执行
	ran, err = env.jobs.RunDue(env.ctx)
	if err != nil || ran {
		t.Fatalf("应无任务可执行: ran=%v err=%v", ran, err)
	}
}

// TestJob_InvalidPayload_RetryExhaustion 非法 payload 重试耗尽后 failed，人工重试重置 pending。
func TestJob_InvalidPayload_RetryExhaustion(t *testing.T) {
	env := newTestEnv(t)
	repo := repository.NewJobRepo(env.db)

	job, err := env.jobs.Enqueue(env.ctx, JobTypeCertMissingScan, map[string]interface{}{"days": 0}, 2)
	if err != nil {
		t.Fatal(err)
	}

	// 第一次执行失败：attempts=1 < 2，安排重试
	ran, err := env.jobs.RunDue(env.ctx)
	if err != nil || !ran {
		t.Fatalf("RunDue: ran=%v err=%v", ran, err)
	}
	got, _ := repo.GetByID(env.ctx, job.ID)
	if got.Status != domain.JobStatusPending || got.Attempts != 1 || got.LastError == "" {
		t.Fatalf("首次失败后状态不符: %+v", got)
	}

	// 将下次执行时间提前到过去，模拟退避到期
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	if _, err := env.db.ExecContext(env.ctx, `UPDATE background_jobs SET run_at = ? WHERE id = ?`, past, job.ID); err != nil {
		t.Fatal(err)
	}

	// 第二次执行失败：attempts=2 耗尽 -> failed
	ran, err = env.jobs.RunDue(env.ctx)
	if err != nil || !ran {
		t.Fatalf("RunDue: ran=%v err=%v", ran, err)
	}
	got, _ = repo.GetByID(env.ctx, job.ID)
	if got.Status != domain.JobStatusFailed || got.Attempts != 2 {
		t.Fatalf("耗尽后应为 failed: %+v", got)
	}

	// failed 任务不再被领取
	ran, err = env.jobs.RunDue(env.ctx)
	if err != nil || ran {
		t.Fatalf("failed 任务不应再执行: ran=%v err=%v", ran, err)
	}

	// 人工重试：重置 pending、清零计数
	job, err = env.jobs.RetryJob(env.ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != domain.JobStatusPending || job.Attempts != 0 {
		t.Fatalf("RetryJob 后状态不符: %+v", job)
	}

	// 非 failed 任务不能重试
	if _, err := env.jobs.RetryJob(env.ctx, job.ID); !domain.IsCode(err, domain.ErrCodeInvalidTransition) {
		t.Fatalf("pending 任务重试应 invalid_transition, 实际 %v", err)
	}
}

// TestJob_UnknownType 未知任务类型执行失败。
func TestJob_UnknownType(t *testing.T) {
	env := newTestEnv(t)
	job, err := env.jobs.Enqueue(env.ctx, "unknown_type", map[string]interface{}{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	ran, err := env.jobs.RunDue(env.ctx)
	if err != nil || !ran {
		t.Fatalf("RunDue: ran=%v err=%v", ran, err)
	}
	got, _ := repository.NewJobRepo(env.db).GetByID(env.ctx, job.ID)
	if got.Status != domain.JobStatusFailed || got.LastError == "" {
		t.Fatalf("未知类型应失败: %+v", got)
	}
}

// TestJob_RecoverOnStartup 遗留 running 任务被重置 pending。
func TestJob_RecoverOnStartup(t *testing.T) {
	env := newTestEnv(t)
	repo := repository.NewJobRepo(env.db)

	job, err := env.jobs.Enqueue(env.ctx, JobTypeCertMissingScan, map[string]interface{}{"days": 30}, 3)
	if err != nil {
		t.Fatal(err)
	}
	// 领取但不完成，模拟进程崩溃遗留 running
	picked, err := repo.PickDue(env.ctx, time.Now().UTC())
	if err != nil || picked == nil {
		t.Fatalf("PickDue: %v %v", picked, err)
	}

	n, err := env.jobs.RecoverOnStartup(env.ctx)
	if err != nil || n != 1 {
		t.Fatalf("RecoverOnStartup: n=%d err=%v", n, err)
	}
	got, _ := repo.GetByID(env.ctx, job.ID)
	if got.Status != domain.JobStatusPending {
		t.Fatalf("应重置为 pending: %+v", got)
	}

	// 恢复后可正常执行完成
	ran, err := env.jobs.RunDue(env.ctx)
	if err != nil || !ran {
		t.Fatalf("恢复后应可执行: ran=%v err=%v", ran, err)
	}
	got, _ = repo.GetByID(env.ctx, job.ID)
	if got.Status != domain.JobStatusDone {
		t.Fatalf("执行后应为 done: %+v", got)
	}
}
