package service

import (
	"testing"
	"time"

	"metalmics/internal/domain"
)

// TestReport_RetestAccepted_CertVersionsFanOut 复验接收归档分页重复回归：
// 两个“初检 fail、复验 pass、已接收”的批次本应各占一行（total=2）。
// 第一批次补录第二版材质证明后，材质证明与批次为多对一，
// 若派生查询未按批次收敛行数，COUNT 与分页查询会出现 fan-out：
// total 变成 3，page_size=1 时第二页重复第一批次，第二批次被挤到第三页。
// 本测试同时校验翻页遍历的批次集合与 total 一致。
func TestReport_RetestAccepted_CertVersionsFanOut(t *testing.T) {
	env := newTestEnv(t)

	// 第一批次：初检 fail，并补录第二版材质证明
	lot1, samples1, _ := env.mustJudgedLot(t, "L-RT-PAG", false)
	task1 := &domain.RetestTask{LotID: lot1.ID, SampleID: samples1[0].ID, Reason: "异议", RequestedBy: "requester"}
	if _, err := env.review.RequestRetest(env.ctx, task1, "requester"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.review.ApproveRetest(env.ctx, task1.ID, 0, "approver"); err != nil {
		t.Fatal(err)
	}
	rep1 := &domain.SpectrumReport{ReportNo: "R-RT-PAG-RET", SampleID: samples1[0].ID, Readings: inRangeReadings(), Analyzer: "tester2"}
	if _, err := env.daily.SubmitSpectrumReport(env.ctx, rep1, "tester2"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.review.ConcludeRetest(env.ctx, task1.ID, 0, domain.ResultPass, "judge2", "co-judge", "覆盖"); err != nil {
		t.Fatal(err)
	}
	// 补录第二版材质证明（judged 非终态，仍允许登记证明）
	cert1b := &domain.MillCertificate{
		CertNo: "C-RT-PAG-V2", LotID: lot1.ID, Grade: lot1.Grade, HeatNo: lot1.HeatNo,
		IssuedAt: time.Now().UTC(),
	}
	if _, err := env.daily.RegisterCertificate(env.ctx, cert1b, "tester"); err != nil {
		t.Fatalf("补录第二版材质证明失败: %v", err)
	}
	if _, err := env.daily.VerifyCertificate(env.ctx, cert1b.ID, 0, "tester"); err != nil {
		t.Fatalf("核对补录证明失败: %v", err)
	}
	if _, err := env.daily.AcceptLot(env.ctx, lot1.ID, 0, "receiver"); err != nil {
		t.Fatalf("接收第一批次失败: %v", err)
	}

	// 第二批次：同样 fail→复验 pass→接收，仅一版证明
	lot2, samples2, _ := env.mustJudgedLot(t, "L-RT-PAG2", false)
	task2 := &domain.RetestTask{LotID: lot2.ID, SampleID: samples2[0].ID, Reason: "异议", RequestedBy: "requester"}
	if _, err := env.review.RequestRetest(env.ctx, task2, "requester"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.review.ApproveRetest(env.ctx, task2.ID, 0, "approver"); err != nil {
		t.Fatal(err)
	}
	rep2 := &domain.SpectrumReport{ReportNo: "R-RT-PAG2-RET", SampleID: samples2[0].ID, Readings: inRangeReadings(), Analyzer: "tester2"}
	if _, err := env.daily.SubmitSpectrumReport(env.ctx, rep2, "tester2"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.review.ConcludeRetest(env.ctx, task2.ID, 0, domain.ResultPass, "judge2", "co-judge", "覆盖"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.daily.AcceptLot(env.ctx, lot2.ID, 0, "receiver"); err != nil {
		t.Fatalf("接收第二批次失败: %v", err)
	}

	// 每页一行翻页：遍历到的批次集合应与 total 一致，且不应跨页重复同一批次
	seen := map[int64]bool{}
	for page := 1; ; page++ {
		p, err := env.report.ListRetestAccepted(env.ctx, domain.PageRequest{Page: page, PageSize: 1, Sort: "id", Order: "asc"})
		if err != nil {
			t.Fatalf("第 %d 页查询失败: %v", page, err)
		}
		if p.Total != 2 {
			t.Fatalf("total = %d, 期望 2（每批次一行）: %+v", p.Total, p)
		}
		if len(p.Items) == 0 {
			break
		}
		for _, row := range p.Items {
			if seen[row.LotID] {
				t.Fatalf("第 %d 页重复出现批次 %d", page, row.LotID)
			}
			seen[row.LotID] = true
		}
		if page > 10 {
			t.Fatalf("翻页超出预期，疑似重复分页")
		}
	}
	if len(seen) != 2 {
		t.Fatalf("翻页批次集合 = %v, 期望两批 {L-RT-PAG, L-RT-PAG2}", seen)
	}

	// 第一页应直接定位第一批次，且证明编号取补录后的最新版本
	p1, err := env.report.ListRetestAccepted(env.ctx, domain.PageRequest{Page: 1, PageSize: 1, Sort: "id", Order: "asc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p1.Items) != 1 || p1.Items[0].LotNo != "L-RT-PAG" {
		t.Fatalf("第一页行不符: %+v", p1)
	}
	if p1.Items[0].CertNo != "C-RT-PAG-V2" {
		t.Fatalf("证明编号应取补录后的最新版本, 实际 = %s", p1.Items[0].CertNo)
	}
}
