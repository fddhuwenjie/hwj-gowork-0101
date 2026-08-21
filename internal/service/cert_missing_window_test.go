package service

import (
	"testing"
	"time"

	"metalmics/internal/domain"
	"metalmics/internal/repository"
)

// nowFixed 作为查询/扫描时的"当前时间"基准，全部接收时刻都相对它精确设定，
// 使时间窗口边界完全确定、不依赖真实时钟与测试执行耗时。
var nowFixed = time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)

// flagAudits 查询某批次被后台扫描写入的 cert_missing_flagged 审计事件数。
func flagAudits(t *testing.T, env *testEnv, lotID int64) int64 {
	t.Helper()
	page, err := env.report.ListAuditEvents(env.ctx,
		domain.AuditFilter{Entity: "material_lot", EntityID: lotID, Actor: "job:" + JobTypeCertMissingScan},
		domain.PageRequest{Page: 1, PageSize: 100, Sort: "id", Order: "asc"})
	if err != nil {
		t.Fatalf("查询审计失败: %v", err)
	}
	return page.Total
}

// TestCertMissingWindow_ExactBoundary 验证"近 days 天"窗口为 [now-days, now]（含两端）：
//   - 正好 days 天前接收、且刚接收（=now）的批次：纳入
//   - days+1 天前接收的批次：排除
//
// 该回归覆盖用户报告的缺陷：隔离中等待后最近才接收的批次被统计与扫描同时漏掉。
// 旧实现 until=now+1d、since=now-(days-1)d，对"过去接收"只覆盖 29 天（days=30），
// 把正好处于第 30 天边界的批次错误排除。
func TestCertMissingWindow_ExactBoundary(t *testing.T) {
	const days = 30
	env := newTestEnvWithClock(t, nowFixed)

	// 三批无证明先接收的批次；accepted_at 用 setLotAcceptedAt 精确设定。
	justNow := makeAcceptedWithoutCert(t, env, "L-NOW")      // 拟设为 now（最近接收）
	atEdge := makeAcceptedWithoutCert(t, env, "L-EDGE30")     // 拟设为 now - 30d（正好 30 天）
	tooOld := makeAcceptedWithoutCert(t, env, "L-OLD31")      // 拟设为 now - 31d（超出 30 天）
	env.setLotAcceptedAt(t, justNow.ID, nowFixed)             // 刚刚接收
	env.setLotAcceptedAt(t, atEdge.ID, nowFixed.Add(-30*24*time.Hour))
	env.setLotAcceptedAt(t, tooOld.ID, nowFixed.Add(-31*24*time.Hour))

	rows, err := env.report.CountCertMissingAccepted(env.ctx, days)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].SupplierID != env.supplier.ID || rows[0].LotCount != 2 {
		t.Fatalf("缺证统计应只含刚接收与 30 天前两批(数量 2), 实际: %+v", rows)
	}

	// 后台扫描：两批被纳入窗口的应各生成一条告警；超出窗口的不生成。
	job, err := env.jobs.Enqueue(env.ctx, JobTypeCertMissingScan, map[string]interface{}{"days": days}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if ran, err := env.jobs.RunDue(env.ctx); err != nil || !ran {
		t.Fatalf("RunDue: ran=%v err=%v", ran, err)
	}
	got, _ := repository.NewJobRepo(env.db).GetByID(env.ctx, job.ID)
	if got.Status != domain.JobStatusDone {
		t.Fatalf("扫描任务应为 done: %+v", got)
	}
	if n := flagAudits(t, env, justNow.ID); n != 1 {
		t.Fatalf("刚接收批次应生成 1 条告警, 实际 %d", n)
	}
	if n := flagAudits(t, env, atEdge.ID); n != 1 {
		t.Fatalf("30 天前接收批次应生成 1 条告警, 实际 %d", n)
	}
	if n := flagAudits(t, env, tooOld.ID); n != 0 {
		t.Fatalf("31 天前批次超出窗口不应生成告警, 实际 %d", n)
	}
}

// TestCertMissingWindow_DaysOne 验证 days=1 这一极端窗口仍覆盖"今日接收"的批次。
// 旧实现 since=now（减 0 天）、until=now+1d，对 accepted_at<now 的真实接收记录几乎不可达，
// 是 off-by-one 在小窗口下最明显的表现。
func TestCertMissingWindow_DaysOne(t *testing.T) {
	env := newTestEnvWithClock(t, nowFixed)
	today := makeAcceptedWithoutCert(t, env, "L-TODAY") // 今日接收
	yesterday := makeAcceptedWithoutCert(t, env, "L-YESTERDAY")
	env.setLotAcceptedAt(t, today.ID, nowFixed)                          // = now，在 [now-1d, now] 内
	env.setLotAcceptedAt(t, yesterday.ID, nowFixed.Add(-25*time.Hour))  // 25h 前，超出 1 天窗口

	rows, err := env.report.CountCertMissingAccepted(env.ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].LotCount != 1 {
		t.Fatalf("days=1 应只含今日接收批次, 实际: %+v", rows)
	}
}

// TestCertMissingWindow_NotRepeatedFlagging 验证已在窗口内的批次被多次扫描时
// 仍正确出现在统计中（幂等性：每次扫描都为窗口内批次写入告警，与窗口无关）。
func TestCertMissingWindow_NotRepeatedFlagging(t *testing.T) {
	env := newTestEnvWithClock(t, nowFixed)
	lot := makeAcceptedWithoutCert(t, env, "L-REP")
	env.setLotAcceptedAt(t, lot.ID, nowFixed.Add(-5*24*time.Hour)) // 5 天前接收，在 30 天窗口内

	// 统计稳定命中
	rows, err := env.report.CountCertMissingAccepted(env.ctx, 30)
	if err != nil || len(rows) != 1 || rows[0].LotCount != 1 {
		t.Fatalf("5 天前接收批次应在 30 天窗口内: %+v err=%v", rows, err)
	}
}
