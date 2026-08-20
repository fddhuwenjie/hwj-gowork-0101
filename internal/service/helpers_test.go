package service

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"metalmics/internal/domain"
	"metalmics/internal/repository"
)

// testEnv 聚合测试所需的服务与上下文。
type testEnv struct {
	ctx       context.Context
	db        *sql.DB
	store     *Store
	daily     *DailyService
	exception *ExceptionService
	review    *ReviewService
	report    *ReportService
	jobs      *JobService
	supplier  *domain.Supplier
}

var testElements = []domain.ElementRange{
	{Element: "Cr", Min: 17, Max: 19},
	{Element: "Ni", Min: 8, Max: 10},
}

func inRangeReadings() []domain.ElementReading {
	return []domain.ElementReading{{Element: "Cr", Value: 18}, {Element: "Ni", Value: 9}}
}

func outOfRangeReadings() []domain.ElementReading {
	return []domain.ElementReading{{Element: "Cr", Value: 20}, {Element: "Ni", Value: 9}}
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	db, err := repository.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	store := NewStore(db)
	env := &testEnv{
		ctx: ctx, db: db, store: store,
		daily:     NewDailyService(store),
		exception: NewExceptionService(store),
		review:    NewReviewService(store),
		report:    NewReportService(store),
		jobs:      NewJobService(store),
	}
	env.supplier = env.mustSupplier(t, "S1")
	env.mustActiveGradeRule(t, "304", 1)
	return env
}

func (e *testEnv) mustSupplier(t *testing.T, code string) *domain.Supplier {
	t.Helper()
	sup := &domain.Supplier{Code: code, Name: "供方" + code}
	created, err := e.daily.RegisterSupplier(e.ctx, sup)
	if err != nil || !created {
		t.Fatalf("登记供方失败: created=%v err=%v", created, err)
	}
	return sup
}

func (e *testEnv) mustActiveGradeRule(t *testing.T, grade string, versionNo int) *domain.GradeRule {
	t.Helper()
	rule := &domain.GradeRule{Grade: grade, VersionNo: versionNo, Elements: testElements}
	created, err := e.daily.CreateGradeRule(e.ctx, rule, "tester")
	if err != nil || !created {
		t.Fatalf("创建规则失败: created=%v err=%v", created, err)
	}
	rule, err = e.daily.ActivateGradeRule(e.ctx, rule.ID, 0, "tester")
	if err != nil {
		t.Fatalf("激活规则失败: %v", err)
	}
	return rule
}

func (e *testEnv) mustRegisterLot(t *testing.T, lotNo, grade string) *domain.MaterialLot {
	t.Helper()
	lot := &domain.MaterialLot{
		LotNo: lotNo, SupplierID: e.supplier.ID, HeatNo: "H-" + lotNo, Grade: grade,
		Quantity: 10, ReceivedAt: time.Now().UTC(),
	}
	created, err := e.daily.RegisterLot(e.ctx, lot, "tester")
	if err != nil || !created {
		t.Fatalf("登记批次失败: created=%v err=%v", created, err)
	}
	return lot
}

// mustSampled 登记计划与样本并完成取样，返回样本（首个留样）。
func (e *testEnv) mustSampled(t *testing.T, lotID int64, planNo string, count int) []*domain.Sample {
	t.Helper()
	plan := &domain.SamplingPlan{
		PlanNo: planNo, LotID: lotID, RequiredCount: count,
		RetainLocation: "留样柜A", CreatedBy: "tester",
	}
	created, err := e.daily.CreateSamplingPlan(e.ctx, plan, "tester")
	if err != nil || !created {
		t.Fatalf("创建取样计划失败: created=%v err=%v", created, err)
	}
	samples := make([]*domain.Sample, 0, count)
	for i := 0; i < count; i++ {
		samples = append(samples, &domain.Sample{
			SampleNo: planNo + "-S" + string(rune('1'+i)),
			Retained: i == 0,
		})
	}
	inserted, err := e.daily.RegisterSamples(e.ctx, plan.ID, samples, "tester")
	if err != nil || inserted != count {
		t.Fatalf("登记样本失败: inserted=%d err=%v", inserted, err)
	}
	if _, err := e.daily.CompleteSampling(e.ctx, lotID, 0, "tester"); err != nil {
		t.Fatalf("取样完成失败: %v", err)
	}
	return samples
}

// mustAnalyzed 为每个样本提交光谱报告并完成分析。
func (e *testEnv) mustAnalyzed(t *testing.T, lotID int64, samples []*domain.Sample, reportPrefix string, inRange bool) {
	t.Helper()
	for i, smp := range samples {
		readings := inRangeReadings()
		if !inRange && i == 0 {
			readings = outOfRangeReadings()
		}
		rep := &domain.SpectrumReport{
			ReportNo: reportPrefix + "-" + string(rune('1'+i)),
			SampleID: smp.ID, Readings: readings, Analyzer: "tester",
		}
		created, err := e.daily.SubmitSpectrumReport(e.ctx, rep, "tester")
		if err != nil || !created {
			t.Fatalf("提交光谱报告失败: created=%v err=%v", created, err)
		}
	}
	if _, err := e.daily.AnalyzeLot(e.ctx, lotID, 0, "tester"); err != nil {
		t.Fatalf("完成分析失败: %v", err)
	}
}

func (e *testEnv) mustVerifiedCert(t *testing.T, lot *domain.MaterialLot, certNo string) *domain.MillCertificate {
	t.Helper()
	cert := &domain.MillCertificate{
		CertNo: certNo, LotID: lot.ID, Grade: lot.Grade, HeatNo: lot.HeatNo,
		IssuedAt: time.Now().UTC(),
	}
	created, err := e.daily.RegisterCertificate(e.ctx, cert, "tester")
	if err != nil || !created {
		t.Fatalf("登记材质证明失败: created=%v err=%v", created, err)
	}
	cert, err = e.daily.VerifyCertificate(e.ctx, cert.ID, 0, "tester")
	if err != nil {
		t.Fatalf("核对材质证明失败: %v", err)
	}
	return cert
}

// mustJudgedLot 构造一个已判定的批次（含 2 个样本、已核对证明），pass 控制结论。
func (e *testEnv) mustJudgedLot(t *testing.T, lotNo string, pass bool) (*domain.MaterialLot, []*domain.Sample, *domain.ConformityConclusion) {
	t.Helper()
	lot := e.mustRegisterLot(t, lotNo, "304")
	samples := e.mustSampled(t, lot.ID, "P-"+lotNo, 2)
	e.mustAnalyzed(t, lot.ID, samples, "R-"+lotNo, pass)
	e.mustVerifiedCert(t, lot, "C-"+lotNo)
	conclusion, err := e.daily.JudgeLot(e.ctx, lot.ID, 0, "judge1", "判定")
	if err != nil {
		t.Fatalf("判定失败: %v", err)
	}
	want := domain.ResultPass
	if !pass {
		want = domain.ResultFail
	}
	if conclusion.Result != want {
		t.Fatalf("判定结果 = %s, 期望 %s", conclusion.Result, want)
	}
	lot, err = e.daily.GetLot(e.ctx, lot.ID)
	if err != nil {
		t.Fatal(err)
	}
	return lot, samples, conclusion
}

func (e *testEnv) getLot(t *testing.T, id int64) *domain.MaterialLot {
	t.Helper()
	lot, err := e.daily.GetLot(e.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return lot
}
