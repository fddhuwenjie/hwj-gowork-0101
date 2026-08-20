package repository

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"metalmics/internal/domain"
)

// openTestDB 在临时目录打开真实 SQLite 文件库。
func openTestDB(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, ctx
}

func mustSupplier(t *testing.T, db *sql.DB, ctx context.Context, code string) *domain.Supplier {
	t.Helper()
	s := &domain.Supplier{Code: code, Name: "供方" + code, Contact: "c"}
	if err := NewSupplierRepo(db).Insert(ctx, s); err != nil {
		t.Fatalf("插入供方失败: %v", err)
	}
	return s
}

func mustLot(t *testing.T, db *sql.DB, ctx context.Context, lotNo string, supplierID int64, grade string) *domain.MaterialLot {
	t.Helper()
	l := &domain.MaterialLot{
		LotNo: lotNo, SupplierID: supplierID, HeatNo: "H-" + lotNo, Grade: grade,
		Quantity: 10, ReceivedAt: time.Now().UTC(),
	}
	if err := NewLotRepo(db).Insert(ctx, l); err != nil {
		t.Fatalf("插入批次失败: %v", err)
	}
	return l
}

func TestOpen_InvalidPath(t *testing.T) {
	if _, err := Open(context.Background(), ""); err == nil {
		t.Fatal("空路径应报错")
	}
}

func TestSupplierRepo_CRUD(t *testing.T) {
	db, ctx := openTestDB(t)
	repo := NewSupplierRepo(db)

	s := &domain.Supplier{Code: "S1", Name: "甲钢厂", Contact: "张三"}
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if s.ID <= 0 || s.Version != 1 || s.CreatedAt.IsZero() {
		t.Fatalf("Insert 未回填字段: %+v", s)
	}

	byID, err := repo.GetByID(ctx, s.ID)
	if err != nil || byID == nil {
		t.Fatalf("GetByID: %v %v", byID, err)
	}
	if byID.Code != "S1" || byID.Name != "甲钢厂" {
		t.Fatalf("GetByID 数据不符: %+v", byID)
	}

	byCode, err := repo.GetByCode(ctx, "S1")
	if err != nil || byCode == nil || byCode.ID != s.ID {
		t.Fatalf("GetByCode: %v %v", byCode, err)
	}

	miss, err := repo.GetByID(ctx, 9999)
	if err != nil || miss != nil {
		t.Fatalf("不存在应返回 nil,nil: %v %v", miss, err)
	}

	// 唯一约束：code 冲突返回领域 Duplicate
	dup := &domain.Supplier{Code: "S1", Name: "乙钢厂"}
	err = repo.Insert(ctx, dup)
	if !domain.IsCode(err, domain.ErrCodeDuplicate) {
		t.Fatalf("期望 duplicate 错误, 实际 %v", err)
	}
}

func TestSupplierRepo_ListFilterSort(t *testing.T) {
	db, ctx := openTestDB(t)
	repo := NewSupplierRepo(db)
	for _, c := range []string{"AB-1", "AB-2", "CD-1"} {
		mustSupplier(t, db, ctx, c)
	}

	page, err := repo.List(ctx, domain.SupplierFilter{CodePrefix: "AB-"}, domain.PageRequest{Page: 1, PageSize: 10, Sort: "code", Order: "asc"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Items) != 2 || page.Items[0].Code != "AB-1" || page.Items[1].Code != "AB-2" {
		t.Fatalf("前缀过滤/排序不符: %+v", page)
	}

	page, err = repo.List(ctx, domain.SupplierFilter{NameLike: "供方"}, domain.PageRequest{Page: 1, PageSize: 10, Sort: "code", Order: "desc"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || page.Items[0].Code != "CD-1" {
		t.Fatalf("名称模糊过滤/降序不符: %+v", page)
	}

	// 分页
	page, err = repo.List(ctx, domain.SupplierFilter{}, domain.PageRequest{Page: 2, PageSize: 2, Sort: "id", Order: "asc"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || len(page.Items) != 1 {
		t.Fatalf("分页不符: %+v", page)
	}
}

func TestGradeRuleRepo_CRUDAndStatus(t *testing.T) {
	db, ctx := openTestDB(t)
	repo := NewGradeRuleRepo(db)

	rule := &domain.GradeRule{
		Grade: "304", VersionNo: 1,
		Elements: []domain.ElementRange{{Element: "Cr", Min: 17, Max: 19}},
	}
	if err := repo.Insert(ctx, rule); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if rule.Status != domain.RuleStatusDraft {
		t.Fatalf("默认状态应为 draft: %s", rule.Status)
	}

	got, err := repo.GetByGradeVersion(ctx, "304", 1)
	if err != nil || got == nil {
		t.Fatalf("GetByGradeVersion: %v %v", got, err)
	}
	if len(got.Elements) != 1 || got.Elements[0].Element != "Cr" || got.Elements[0].Min != 17 {
		t.Fatalf("元素区间往返不符: %+v", got.Elements)
	}

	// (grade, version_no) 唯一
	dup := &domain.GradeRule{Grade: "304", VersionNo: 1, Elements: []domain.ElementRange{{Element: "Ni", Min: 8, Max: 10}}}
	if err := repo.Insert(ctx, dup); !domain.IsCode(err, domain.ErrCodeDuplicate) {
		t.Fatalf("期望 duplicate, 实际 %v", err)
	}

	// 无 active 时返回 nil
	active, err := repo.GetActiveByGrade(ctx, "304")
	if err != nil || active != nil {
		t.Fatalf("期望无 active: %v %v", active, err)
	}

	// 乐观锁激活
	ok, err := repo.UpdateStatus(ctx, rule.ID, domain.RuleStatusActive, rule.Version)
	if err != nil || !ok {
		t.Fatalf("UpdateStatus: ok=%v err=%v", ok, err)
	}
	// 错误版本
	ok, err = repo.UpdateStatus(ctx, rule.ID, domain.RuleStatusRetired, rule.Version) // 旧版本
	if err != nil || ok {
		t.Fatalf("旧版本应更新 0 行: ok=%v err=%v", ok, err)
	}

	// RetireActiveByGrade
	if err := repo.RetireActiveByGrade(ctx, "304"); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.GetByID(ctx, rule.ID)
	if got.Status != domain.RuleStatusRetired {
		t.Fatalf("期望 retired: %s", got.Status)
	}
}

func TestLotRepo_CRUDAndTransition(t *testing.T) {
	db, ctx := openTestDB(t)
	sup := mustSupplier(t, db, ctx, "S1")
	repo := NewLotRepo(db)

	lot := &domain.MaterialLot{
		LotNo: "L1", SupplierID: sup.ID, HeatNo: "H1", Grade: "304",
		Quantity: 12.5, ReceivedAt: time.Now().UTC(),
	}
	if err := repo.Insert(ctx, lot); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if lot.Status != domain.LotStatusRegistered || lot.Version != 1 {
		t.Fatalf("默认值不符: %+v", lot)
	}

	got, err := repo.GetByLotNo(ctx, "L1")
	if err != nil || got == nil {
		t.Fatalf("GetByLotNo: %v %v", got, err)
	}
	if got.Quantity != 12.5 || got.HeatNo != "H1" || got.Grade != "304" {
		t.Fatalf("数据不符: %+v", got)
	}

	// lot_no 唯一
	dup := &domain.MaterialLot{LotNo: "L1", SupplierID: sup.ID, HeatNo: "H9", Grade: "304", Quantity: 1, ReceivedAt: time.Now().UTC()}
	if err := repo.Insert(ctx, dup); !domain.IsCode(err, domain.ErrCodeDuplicate) {
		t.Fatalf("期望 duplicate, 实际 %v", err)
	}

	// 外键：不存在的供方
	bad := &domain.MaterialLot{LotNo: "L-FK", SupplierID: 9999, HeatNo: "H", Grade: "304", Quantity: 1, ReceivedAt: time.Now().UTC()}
	if err := repo.Insert(ctx, bad); err == nil {
		t.Fatal("外键约束应阻止插入")
	}

	// Transition 乐观锁：正确版本成功且版本递增、可更新结论字段
	pass := "pass"
	ok, err := repo.Transition(ctx, lot.ID, lot.Version, domain.LotStatusJudged, &pass, nil, nil, nil)
	if err != nil || !ok {
		t.Fatalf("Transition: ok=%v err=%v", ok, err)
	}
	got, _ = repo.GetByID(ctx, lot.ID)
	if got.Status != domain.LotStatusJudged || got.Version != lot.Version+1 || got.InitialResult != "pass" {
		t.Fatalf("Transition 结果不符: %+v", got)
	}
	// 错误版本返回 false
	ok, err = repo.Transition(ctx, lot.ID, lot.Version, domain.LotStatusAccepted, nil, nil, nil, nil)
	if err != nil || ok {
		t.Fatalf("版本冲突应返回 false: ok=%v err=%v", ok, err)
	}
	// 不存在 id
	ok, err = repo.Transition(ctx, 9999, 1, domain.LotStatusAccepted, nil, nil, nil, nil)
	if err != nil || ok {
		t.Fatalf("不存在记录应返回 false: ok=%v err=%v", ok, err)
	}
}

func TestLotRepo_ListFilterSortStable(t *testing.T) {
	db, ctx := openTestDB(t)
	s1 := mustSupplier(t, db, ctx, "S1")
	s2 := mustSupplier(t, db, ctx, "S2")
	repo := NewLotRepo(db)

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lots := []struct {
		no     string
		sup    int64
		grade  string
		status domain.LotStatus
		recv   time.Time
	}{
		{"A-1", s1.ID, "304", domain.LotStatusRegistered, base},
		{"A-2", s1.ID, "304", domain.LotStatusSampled, base.Add(24 * time.Hour)},
		{"B-1", s2.ID, "316", domain.LotStatusSampled, base.Add(48 * time.Hour)},
		{"B-2", s2.ID, "316", domain.LotStatusAccepted, base.Add(72 * time.Hour)},
	}
	for _, x := range lots {
		l := &domain.MaterialLot{LotNo: x.no, SupplierID: x.sup, HeatNo: "H", Grade: x.grade,
			Quantity: 1, Status: x.status, ReceivedAt: x.recv}
		if err := repo.Insert(ctx, l); err != nil {
			t.Fatal(err)
		}
	}

	// 状态过滤
	page, err := repo.List(ctx, domain.LotFilter{Status: domain.LotStatusSampled}, domain.PageRequest{Page: 1, PageSize: 10, Sort: "id", Order: "asc"})
	if err != nil || page.Total != 2 {
		t.Fatalf("状态过滤不符: %+v err=%v", page, err)
	}

	// 供方 + 牌号过滤
	page, _ = repo.List(ctx, domain.LotFilter{SupplierID: s2.ID, Grade: "316"}, domain.PageRequest{Page: 1, PageSize: 10, Sort: "id", Order: "asc"})
	if page.Total != 2 {
		t.Fatalf("供方+牌号过滤不符: %+v", page)
	}

	// 批次号前缀
	page, _ = repo.List(ctx, domain.LotFilter{LotNoPrefix: "A-"}, domain.PageRequest{Page: 1, PageSize: 10, Sort: "id", Order: "asc"})
	if page.Total != 2 {
		t.Fatalf("前缀过滤不符: %+v", page)
	}

	// 到货时间区间
	after := base.Add(12 * time.Hour)
	before := base.Add(60 * time.Hour)
	page, _ = repo.List(ctx, domain.LotFilter{ReceivedAfter: &after, ReceivedBefore: &before}, domain.PageRequest{Page: 1, PageSize: 10, Sort: "id", Order: "asc"})
	if page.Total != 2 {
		t.Fatalf("时间区间过滤不符: %+v", page)
	}

	// 稳定排序：按 status 排序时同键按 id 升序
	page, err = repo.List(ctx, domain.LotFilter{}, domain.PageRequest{Page: 1, PageSize: 10, Sort: "status", Order: "asc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 4 {
		t.Fatalf("期望 4 行: %d", len(page.Items))
	}
	for i := 1; i < len(page.Items); i++ {
		prev, cur := page.Items[i-1], page.Items[i]
		if cur.Status < prev.Status {
			t.Fatalf("排序键乱序: %v", page.Items)
		}
		if cur.Status == prev.Status && cur.ID < prev.ID {
			t.Fatalf("稳定排序失效（同键未按 id 升序）: %v", page.Items)
		}
	}

	// 分页遍历不重复不遗漏
	seen := map[int64]bool{}
	for p := 1; p <= 2; p++ {
		page, _ = repo.List(ctx, domain.LotFilter{}, domain.PageRequest{Page: p, PageSize: 2, Sort: "id", Order: "asc"})
		for _, it := range page.Items {
			if seen[it.ID] {
				t.Fatalf("分页出现重复 id %d", it.ID)
			}
			seen[it.ID] = true
		}
	}
	if len(seen) != 4 {
		t.Fatalf("分页遗漏: %v", seen)
	}
}

func TestSamplingPlanAndSampleRepo(t *testing.T) {
	db, ctx := openTestDB(t)
	sup := mustSupplier(t, db, ctx, "S1")
	lot := mustLot(t, db, ctx, "L1", sup.ID, "304")

	planRepo := NewSamplingPlanRepo(db)
	plan := &domain.SamplingPlan{PlanNo: "P1", LotID: lot.ID, RequiredCount: 2, RetainLocation: "柜A"}
	if err := planRepo.Insert(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if plan.Status != domain.PlanStatusActive {
		t.Fatalf("默认状态应为 active: %s", plan.Status)
	}
	// 每批次一份计划（lot_id 唯一）
	dup := &domain.SamplingPlan{PlanNo: "P2", LotID: lot.ID, RequiredCount: 1, RetainLocation: "柜B"}
	if err := planRepo.Insert(ctx, dup); !domain.IsCode(err, domain.ErrCodeDuplicate) {
		t.Fatalf("期望 duplicate, 实际 %v", err)
	}
	got, err := planRepo.GetByLot(ctx, lot.ID)
	if err != nil || got == nil || got.PlanNo != "P1" {
		t.Fatalf("GetByLot: %v %v", got, err)
	}
	ok, err := planRepo.UpdateStatus(ctx, plan.ID, plan.Version, domain.PlanStatusCompleted)
	if err != nil || !ok {
		t.Fatalf("UpdateStatus: ok=%v err=%v", ok, err)
	}
	ok, _ = planRepo.UpdateStatus(ctx, plan.ID, plan.Version, domain.PlanStatusCancelled)
	if ok {
		t.Fatal("旧版本应更新失败")
	}

	sampleRepo := NewSampleRepo(db)
	s1 := &domain.Sample{PlanID: plan.ID, SampleNo: "SMP-1", Kind: domain.SampleKindInitial, Retained: true}
	if err := sampleRepo.Insert(ctx, s1); err != nil {
		t.Fatal(err)
	}
	if s1.Status != domain.SampleStatusCreated {
		t.Fatalf("默认状态应为 created: %s", s1.Status)
	}
	// (plan_id, sample_no) 唯一
	dupS := &domain.Sample{PlanID: plan.ID, SampleNo: "SMP-1", Kind: domain.SampleKindInitial}
	if err := sampleRepo.Insert(ctx, dupS); !domain.IsCode(err, domain.ErrCodeDuplicate) {
		t.Fatalf("期望 duplicate, 实际 %v", err)
	}
	s2 := &domain.Sample{PlanID: plan.ID, SampleNo: "SMP-2", Kind: domain.SampleKindInitial}
	if err := sampleRepo.Insert(ctx, s2); err != nil {
		t.Fatal(err)
	}
	n, err := sampleRepo.CountByPlanAndKind(ctx, plan.ID, domain.SampleKindInitial)
	if err != nil || n != 2 {
		t.Fatalf("CountByPlanAndKind: %d %v", n, err)
	}
	list, err := sampleRepo.ListByPlan(ctx, plan.ID)
	if err != nil || len(list) != 2 || list[0].ID >= list[1].ID {
		t.Fatalf("ListByPlan 不符: %v %v", list, err)
	}
	if !list[0].Retained {
		t.Fatal("Retained 往返不符")
	}
	byNo, err := sampleRepo.GetByPlanAndNo(ctx, plan.ID, "SMP-2")
	if err != nil || byNo == nil || byNo.ID != s2.ID {
		t.Fatalf("GetByPlanAndNo: %v %v", byNo, err)
	}
	ok, err = sampleRepo.UpdateStatus(ctx, s1.ID, s1.Version, domain.SampleStatusTested)
	if err != nil || !ok {
		t.Fatalf("样本 UpdateStatus: ok=%v err=%v", ok, err)
	}
}

func TestCertificateRepo(t *testing.T) {
	db, ctx := openTestDB(t)
	sup := mustSupplier(t, db, ctx, "S1")
	lot := mustLot(t, db, ctx, "L1", sup.ID, "304")
	repo := NewCertificateRepo(db)

	cert := &domain.MillCertificate{
		CertNo: "C1", LotID: lot.ID, Grade: "304", HeatNo: "H1",
		Elements: []domain.ElementRange{{Element: "Cr", Min: 18, Max: 18}},
		IssuedAt: time.Now().UTC(),
	}
	if err := repo.Insert(ctx, cert); err != nil {
		t.Fatal(err)
	}
	if cert.Verified {
		t.Fatal("新证明不应已核对")
	}
	dup := &domain.MillCertificate{CertNo: "C1", LotID: lot.ID, Grade: "304", HeatNo: "H1", IssuedAt: time.Now().UTC()}
	if err := repo.Insert(ctx, dup); !domain.IsCode(err, domain.ErrCodeDuplicate) {
		t.Fatalf("期望 duplicate, 实际 %v", err)
	}

	got, err := repo.GetByCertNo(ctx, "C1")
	if err != nil || got == nil || got.LotID != lot.ID {
		t.Fatalf("GetByCertNo: %v %v", got, err)
	}

	ok, err := repo.MarkVerified(ctx, cert.ID, cert.Version, "tester", time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("MarkVerified: ok=%v err=%v", ok, err)
	}
	latest, err := repo.LatestByLot(ctx, lot.ID)
	if err != nil || latest == nil || !latest.Verified || latest.VerifiedBy != "tester" || latest.VerifiedAt == nil {
		t.Fatalf("LatestByLot: %+v err=%v", latest, err)
	}
	// 旧版本失败
	ok, _ = repo.MarkVerified(ctx, cert.ID, cert.Version, "x", time.Now().UTC())
	if ok {
		t.Fatal("旧版本 MarkVerified 应失败")
	}

	certs, err := repo.ListByLot(ctx, lot.ID)
	if err != nil || len(certs) != 1 {
		t.Fatalf("ListByLot: %v %v", certs, err)
	}
}

func TestSpectrumRepo(t *testing.T) {
	db, ctx := openTestDB(t)
	sup := mustSupplier(t, db, ctx, "S1")
	lot := mustLot(t, db, ctx, "L1", sup.ID, "304")
	rule := &domain.GradeRule{Grade: "304", VersionNo: 1, Elements: []domain.ElementRange{{Element: "Cr", Min: 17, Max: 19}}}
	if err := NewGradeRuleRepo(db).Insert(ctx, rule); err != nil {
		t.Fatal(err)
	}
	plan := &domain.SamplingPlan{PlanNo: "P1", LotID: lot.ID, RequiredCount: 1, RetainLocation: "柜A"}
	if err := NewSamplingPlanRepo(db).Insert(ctx, plan); err != nil {
		t.Fatal(err)
	}
	smp := &domain.Sample{PlanID: plan.ID, SampleNo: "SMP-1", Kind: domain.SampleKindInitial, Retained: true}
	if err := NewSampleRepo(db).Insert(ctx, smp); err != nil {
		t.Fatal(err)
	}

	repo := NewSpectrumRepo(db)
	rep := &domain.SpectrumReport{
		ReportNo: "R1", SampleID: smp.ID, RuleID: rule.ID,
		Readings: []domain.ElementReading{{Element: "Cr", Value: 18}},
		Conclusion: domain.SpectrumInRange, Analyzer: "tester",
	}
	if err := repo.Insert(ctx, rep); err != nil {
		t.Fatal(err)
	}
	dup := &domain.SpectrumReport{ReportNo: "R1", SampleID: smp.ID, RuleID: rule.ID,
		Readings: []domain.ElementReading{{Element: "Cr", Value: 18}}, Conclusion: domain.SpectrumInRange, Analyzer: "x"}
	if err := repo.Insert(ctx, dup); !domain.IsCode(err, domain.ErrCodeDuplicate) {
		t.Fatalf("期望 duplicate, 实际 %v", err)
	}

	got, err := repo.GetByReportNo(ctx, "R1")
	if err != nil || got == nil || len(got.Readings) != 1 || got.Readings[0].Value != 18 {
		t.Fatalf("GetByReportNo: %+v err=%v", got, err)
	}
	byLot, err := repo.ListByLot(ctx, lot.ID)
	if err != nil || len(byLot) != 1 {
		t.Fatalf("ListByLot: %v %v", byLot, err)
	}
	byKind, err := repo.ListBySampleKind(ctx, lot.ID, domain.SampleKindInitial)
	if err != nil || len(byKind) != 1 {
		t.Fatalf("ListBySampleKind: %v %v", byKind, err)
	}
	byKind, _ = repo.ListBySampleKind(ctx, lot.ID, domain.SampleKindRetest)
	if len(byKind) != 0 {
		t.Fatalf("复验报告应为空: %v", byKind)
	}
	bySample, err := repo.ListBySample(ctx, smp.ID)
	if err != nil || len(bySample) != 1 {
		t.Fatalf("ListBySample: %v %v", bySample, err)
	}
}

func TestConclusionRepo(t *testing.T) {
	db, ctx := openTestDB(t)
	sup := mustSupplier(t, db, ctx, "S1")
	lot := mustLot(t, db, ctx, "L1", sup.ID, "304")
	repo := NewConclusionRepo(db)

	c := &domain.ConformityConclusion{
		LotID: lot.ID, Round: domain.RoundInitial, Result: domain.ResultFail,
		CertOK: true, SpectrumOK: false, Reason: "r", DecidedBy: "tester",
	}
	if err := repo.Insert(ctx, c); err != nil {
		t.Fatal(err)
	}
	// (lot_id, round) 唯一
	dup := &domain.ConformityConclusion{LotID: lot.ID, Round: domain.RoundInitial, Result: domain.ResultPass, DecidedBy: "x"}
	if err := repo.Insert(ctx, dup); !domain.IsCode(err, domain.ErrCodeDuplicate) {
		t.Fatalf("期望 duplicate, 实际 %v", err)
	}
	got, err := repo.GetByLotRound(ctx, lot.ID, domain.RoundInitial)
	if err != nil || got == nil || got.Result != domain.ResultFail || !got.CertOK || got.SpectrumOK {
		t.Fatalf("GetByLotRound: %+v err=%v", got, err)
	}
	miss, err := repo.GetByLotRound(ctx, lot.ID, domain.RoundRetest)
	if err != nil || miss != nil {
		t.Fatalf("无复验结论应 nil: %v %v", miss, err)
	}
	list, err := repo.ListByLot(ctx, lot.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListByLot: %v %v", list, err)
	}
}

func TestRetestRepo(t *testing.T) {
	db, ctx := openTestDB(t)
	sup := mustSupplier(t, db, ctx, "S1")
	lot := mustLot(t, db, ctx, "L1", sup.ID, "304")
	plan := &domain.SamplingPlan{PlanNo: "P1", LotID: lot.ID, RequiredCount: 1, RetainLocation: "柜A"}
	if err := NewSamplingPlanRepo(db).Insert(ctx, plan); err != nil {
		t.Fatal(err)
	}
	smp := &domain.Sample{PlanID: plan.ID, SampleNo: "SMP-1", Kind: domain.SampleKindInitial, Retained: true}
	if err := NewSampleRepo(db).Insert(ctx, smp); err != nil {
		t.Fatal(err)
	}

	repo := NewRetestRepo(db)
	task := &domain.RetestTask{LotID: lot.ID, SampleID: smp.ID, Reason: "异议", RequestedBy: "tester"}
	if err := repo.Insert(ctx, task); err != nil {
		t.Fatal(err)
	}
	if task.Status != domain.RetestStatusOpen {
		t.Fatalf("默认状态应为 open: %s", task.Status)
	}
	// 同批次仅一个未关闭任务
	dup := &domain.RetestTask{LotID: lot.ID, SampleID: smp.ID, Reason: "x", RequestedBy: "y"}
	if err := repo.Insert(ctx, dup); !domain.IsCode(err, domain.ErrCodeDuplicate) {
		t.Fatalf("期望 duplicate, 实际 %v", err)
	}
	open, err := repo.GetOpenByLot(ctx, lot.ID)
	if err != nil || open == nil || open.ID != task.ID {
		t.Fatalf("GetOpenByLot: %v %v", open, err)
	}

	approver := "boss"
	ok, err := repo.UpdateStatus(ctx, task.ID, task.Version, domain.RetestStatusApproved, &approver)
	if err != nil || !ok {
		t.Fatalf("UpdateStatus: ok=%v err=%v", ok, err)
	}
	got, _ := repo.GetByID(ctx, task.ID)
	if got.Status != domain.RetestStatusApproved || got.ApprovedBy != "boss" {
		t.Fatalf("更新不符: %+v", got)
	}

	page, err := repo.List(ctx, domain.RetestFilter{LotID: lot.ID, Status: domain.RetestStatusApproved},
		domain.PageRequest{Page: 1, PageSize: 10, Sort: "id", Order: "asc"})
	if err != nil || page.Total != 1 {
		t.Fatalf("List: %+v err=%v", page, err)
	}
}

func TestDispositionRepo(t *testing.T) {
	db, ctx := openTestDB(t)
	sup := mustSupplier(t, db, ctx, "S1")
	lot := mustLot(t, db, ctx, "L1", sup.ID, "304")
	repo := NewDispositionRepo(db)

	d := &domain.Disposition{LotID: lot.ID, Type: domain.DispositionConcession, Reason: "让步", ProposedBy: "tester"}
	if err := repo.Insert(ctx, d); err != nil {
		t.Fatal(err)
	}
	if d.Status != domain.DispositionProposed {
		t.Fatalf("默认状态应为 proposed: %s", d.Status)
	}
	// 同批次同类型仅一个未关闭单
	dup := &domain.Disposition{LotID: lot.ID, Type: domain.DispositionConcession, Reason: "x", ProposedBy: "y"}
	if err := repo.Insert(ctx, dup); !domain.IsCode(err, domain.ErrCodeDuplicate) {
		t.Fatalf("期望 duplicate, 实际 %v", err)
	}

	has, err := repo.HasApprovedConcession(ctx, lot.ID)
	if err != nil || has {
		t.Fatalf("未批准时应为 false: %v %v", has, err)
	}
	approver := "boss"
	ok, err := repo.UpdateStatus(ctx, d.ID, d.Version, domain.DispositionApproved, nil, &approver, nil)
	if err != nil || !ok {
		t.Fatalf("UpdateStatus: ok=%v err=%v", ok, err)
	}
	has, _ = repo.HasApprovedConcession(ctx, lot.ID)
	if !has {
		t.Fatal("批准后应为 true")
	}
	open, err := repo.GetOpenByLotType(ctx, lot.ID, domain.DispositionConcession)
	if err != nil || open == nil || open.ID != d.ID {
		t.Fatalf("GetOpenByLotType: %v %v", open, err)
	}
	page, err := repo.List(ctx, domain.DispositionFilter{LotID: lot.ID},
		domain.PageRequest{Page: 1, PageSize: 10, Sort: "id", Order: "asc"})
	if err != nil || page.Total != 1 {
		t.Fatalf("List: %+v err=%v", page, err)
	}
}

func TestJobRepo(t *testing.T) {
	db, ctx := openTestDB(t)
	repo := NewJobRepo(db)

	job := &domain.BackgroundJob{Type: "cert_missing_scan", Payload: `{"days":30}`, MaxAttempts: 2}
	if err := repo.Insert(ctx, job); err != nil {
		t.Fatal(err)
	}
	if job.Status != domain.JobStatusPending {
		t.Fatalf("默认状态应为 pending: %s", job.Status)
	}

	// PickDue 领取并置 running
	picked, err := repo.PickDue(ctx, time.Now().UTC())
	if err != nil || picked == nil {
		t.Fatalf("PickDue: %v %v", picked, err)
	}
	if picked.Status != domain.JobStatusRunning || picked.Attempts != 1 {
		t.Fatalf("PickDue 状态不符: %+v", picked)
	}
	// 无其他任务可领
	again, err := repo.PickDue(ctx, time.Now().UTC())
	if err != nil || again != nil {
		t.Fatalf("应无任务可领: %v %v", again, err)
	}

	// RequeueRunning
	n, err := repo.RequeueRunning(ctx)
	if err != nil || n != 1 {
		t.Fatalf("RequeueRunning: %d %v", n, err)
	}
	got, _ := repo.GetByID(ctx, job.ID)
	if got.Status != domain.JobStatusPending {
		t.Fatalf("应重置为 pending: %s", got.Status)
	}

	// MarkRetry 未耗尽 -> pending；耗尽 -> failed
	if err := repo.MarkRetry(ctx, job.ID, "boom", time.Now().UTC().Add(-time.Second), false); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.GetByID(ctx, job.ID)
	if got.Status != domain.JobStatusPending || got.LastError != "boom" {
		t.Fatalf("MarkRetry 未耗尽不符: %+v", got)
	}
	if err := repo.MarkRetry(ctx, job.ID, "boom", time.Now().UTC(), true); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.GetByID(ctx, job.ID)
	if got.Status != domain.JobStatusFailed {
		t.Fatalf("MarkRetry 耗尽应为 failed: %+v", got)
	}

	// RetryFailed
	ok, err := repo.RetryFailed(ctx, job.ID)
	if err != nil || !ok {
		t.Fatalf("RetryFailed: ok=%v err=%v", ok, err)
	}
	got, _ = repo.GetByID(ctx, job.ID)
	if got.Status != domain.JobStatusPending || got.Attempts != 0 || got.LastError != "" {
		t.Fatalf("RetryFailed 后不符: %+v", got)
	}

	// MarkDone
	if err := repo.MarkDone(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.GetByID(ctx, job.ID)
	if got.Status != domain.JobStatusDone {
		t.Fatalf("应为 done: %s", got.Status)
	}

	// 未来任务不被领取
	future := &domain.BackgroundJob{Type: "t2", Payload: "{}", MaxAttempts: 1, RunAt: time.Now().UTC().Add(time.Hour)}
	if err := repo.Insert(ctx, future); err != nil {
		t.Fatal(err)
	}
	picked, err = repo.PickDue(ctx, time.Now().UTC())
	if err != nil || picked != nil {
		t.Fatalf("未到期任务不应被领取: %v %v", picked, err)
	}
}

func TestAuditRepo(t *testing.T) {
	db, ctx := openTestDB(t)
	repo := NewAuditRepo(db)

	e1 := &domain.AuditEvent{Entity: "material_lot", EntityID: 1, Action: "register", Actor: "tester", Detail: `{"a":1}`}
	e2 := &domain.AuditEvent{Entity: "material_lot", EntityID: 1, Action: "accept", Actor: "boss", Detail: "{}"}
	e3 := &domain.AuditEvent{Entity: "supplier", EntityID: 2, Action: "register", Actor: "tester", Detail: "{}"}
	for _, e := range []*domain.AuditEvent{e1, e2, e3} {
		if err := repo.Insert(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	page, err := repo.List(ctx, domain.AuditFilter{Entity: "material_lot", EntityID: 1},
		domain.PageRequest{Page: 1, PageSize: 10, Sort: "id", Order: "asc"})
	if err != nil || page.Total != 2 {
		t.Fatalf("实体过滤不符: %+v err=%v", page, err)
	}
	if page.Items[0].Action != "register" || page.Items[1].Action != "accept" {
		t.Fatalf("审计顺序不符: %v", page.Items)
	}
	page, _ = repo.List(ctx, domain.AuditFilter{Actor: "boss"},
		domain.PageRequest{Page: 1, PageSize: 10, Sort: "id", Order: "asc"})
	if page.Total != 1 {
		t.Fatalf("操作人过滤不符: %+v", page)
	}
	since := time.Now().UTC().Add(time.Hour)
	page, _ = repo.List(ctx, domain.AuditFilter{Since: &since},
		domain.PageRequest{Page: 1, PageSize: 10, Sort: "id", Order: "asc"})
	if page.Total != 0 {
		t.Fatalf("时间过滤不符: %+v", page)
	}
}

func TestReopenPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	sup := mustSupplier(t, db, ctx, "S1")
	mustLot(t, db, ctx, "L1", sup.ID, "304")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// 同一路径重开，数据仍在
	db2, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	got, err := NewLotRepo(db2).GetByLotNo(ctx, "L1")
	if err != nil || got == nil {
		t.Fatalf("重开后数据丢失: %v %v", got, err)
	}
	sup2, err := NewSupplierRepo(db2).GetByCode(ctx, "S1")
	if err != nil || sup2 == nil || sup2.ID != sup.ID {
		t.Fatalf("重开后供方数据不符: %v %v", sup2, err)
	}
}

func TestTxManager_Rollback(t *testing.T) {
	db, ctx := openTestDB(t)
	tm := NewTxManager(db)
	sup := mustSupplier(t, db, ctx, "S1")

	errBoom := domain.Validation("x", "boom")
	err := tm.InTx(ctx, func(tx *sql.Tx) error {
		lot := &domain.MaterialLot{LotNo: "L-TX", SupplierID: sup.ID, HeatNo: "H", Grade: "304",
			Quantity: 1, ReceivedAt: time.Now().UTC()}
		if err := NewLotRepo(tx).Insert(ctx, lot); err != nil {
			return err
		}
		return errBoom // 强制回滚
	})
	if err != errBoom {
		t.Fatalf("应透传业务错误: %v", err)
	}
	got, err := NewLotRepo(db).GetByLotNo(ctx, "L-TX")
	if err != nil || got != nil {
		t.Fatalf("回滚后不应存在部分写入: %v %v", got, err)
	}
}
