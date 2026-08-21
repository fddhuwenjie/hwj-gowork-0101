package service

import (
	"sync"
	"testing"
	"time"

	"metalmics/internal/domain"
)

// TestConcurrentAcceptLot 两个 goroutine 同时对同一批次 AcceptLot（携带正确版本），
// 最终只有一处成功，另一处报版本冲突或非法转换。
func TestConcurrentAcceptLot(t *testing.T) {
	env := newTestEnv(t)
	lot, _, _ := env.mustJudgedLot(t, "L-RACE", true)

	const workers = 2
	errs := make([]error, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			_, errs[idx] = env.daily.AcceptLot(env.ctx, lot.ID, lot.Version, "receiver")
		}(i)
	}
	close(start)
	wg.Wait()

	var succeeded, failed int
	for _, err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		failed++
		if !domain.IsCode(err, domain.ErrCodeVersionConflict) && !domain.IsCode(err, domain.ErrCodeInvalidTransition) {
			t.Fatalf("失败方应报版本冲突或非法转换, 实际 %v", err)
		}
	}
	if succeeded != 1 || failed != 1 {
		t.Fatalf("期望恰好一处成功一处失败: succeeded=%d failed=%d errs=%v", succeeded, failed, errs)
	}

	lot = env.getLot(t, lot.ID)
	if lot.Status != domain.LotStatusAccepted || lot.AcceptedBy != "receiver" {
		t.Fatalf("最终状态不符: %+v", lot)
	}
	// 只应有一条 accept 审计
	audits, err := env.report.ListAuditEvents(env.ctx, domain.AuditFilter{Entity: "material_lot", EntityID: lot.ID},
		domain.PageRequest{Page: 1, PageSize: 100, Sort: "id", Order: "asc"})
	if err != nil {
		t.Fatal(err)
	}
	accepts := 0
	for _, a := range audits.Items {
		if a.Action == "accept" {
			accepts++
		}
	}
	if accepts != 1 {
		t.Fatalf("accept 审计应恰好一条: %d", accepts)
	}
}

// TestConcurrentVerifyCertificate 两个 goroutine 同时核对同一未核对证明（携带正确版本），
// 最终只有一处成功核对，另一处报版本冲突（409），且核对人/版本只被写入一次。
func TestConcurrentVerifyCertificate(t *testing.T) {
	env := newTestEnv(t)
	lot := env.mustRegisterLot(t, "L-CRACE", "304")
	cert := &domain.MillCertificate{CertNo: "C-CRACE", LotID: lot.ID, Grade: "304", HeatNo: lot.HeatNo, IssuedAt: time.Now().UTC()}
	if _, err := env.daily.RegisterCertificate(env.ctx, cert, "tester"); err != nil {
		t.Fatal(err)
	}

	const workers = 2
	errs := make([]error, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			_, errs[idx] = env.daily.VerifyCertificate(env.ctx, cert.ID, cert.Version, "verifier")
		}(i)
	}
	close(start)
	wg.Wait()

	var succeeded, failed int
	for _, err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		failed++
		if !domain.IsCode(err, domain.ErrCodeVersionConflict) {
			t.Fatalf("失败方应报版本冲突, 实际 %v", err)
		}
	}
	if succeeded != 1 || failed != 1 {
		t.Fatalf("期望恰好一处成功一处失败: succeeded=%d failed=%d errs=%v", succeeded, failed, errs)
	}

	// 核对只成功一次：版本恰好自增 1，verify 审计恰好一条
	got, err := env.daily.ListCertificates(env.ctx, lot.ID)
	if err != nil || len(got) != 1 {
		t.Fatalf("ListCertificates: %v %v", got, err)
	}
	if !got[0].Verified || got[0].Version != cert.Version+1 {
		t.Fatalf("最终状态不符: %+v", got[0])
	}
	audits, err := env.report.ListAuditEvents(env.ctx, domain.AuditFilter{Entity: "mill_certificate", EntityID: cert.ID},
		domain.PageRequest{Page: 1, PageSize: 100, Sort: "id", Order: "asc"})
	if err != nil {
		t.Fatal(err)
	}
	verifies := 0
	for _, a := range audits.Items {
		if a.Action == "verify" {
			verifies++
		}
	}
	if verifies != 1 {
		t.Fatalf("verify 审计应恰好一条: %d", verifies)
	}
}
