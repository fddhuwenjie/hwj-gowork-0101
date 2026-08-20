package domain

import (
	"strings"
	"testing"
	"time"
)

// expectedLotTransitions 与生产代码 lotTransitions 保持一致的白名单，
// 用于对 8 个状态做两两组合（64 组）的穷举校验。
var expectedLotTransitions = map[LotStatus][]LotStatus{
	LotStatusRegistered:  {LotStatusSampled, LotStatusQuarantined},
	LotStatusSampled:     {LotStatusAnalyzed, LotStatusQuarantined},
	LotStatusAnalyzed:    {LotStatusJudged, LotStatusQuarantined},
	LotStatusJudged:      {LotStatusAccepted, LotStatusRejected, LotStatusRetesting, LotStatusQuarantined},
	LotStatusRetesting:   {LotStatusJudged, LotStatusQuarantined},
	LotStatusRejected:    {LotStatusRetesting},
	LotStatusAccepted:    {},
	LotStatusQuarantined: {LotStatusRejected, LotStatusAccepted, LotStatusRetesting},
}

func TestAllLotStatuses(t *testing.T) {
	all := AllLotStatuses()
	if len(all) != 8 {
		t.Fatalf("期望 8 个状态, 实际 %d", len(all))
	}
	seen := map[LotStatus]bool{}
	for _, s := range all {
		if seen[s] {
			t.Fatalf("状态 %s 重复", s)
		}
		seen[s] = true
	}
}

func TestLotStatusTransitions_Exhaustive(t *testing.T) {
	all := AllLotStatuses()
	for _, from := range all {
		legal := map[LotStatus]bool{}
		for _, to := range expectedLotTransitions[from] {
			legal[to] = true
		}
		for _, to := range all {
			got := from.CanTransitionTo(to)
			want := legal[to]
			if got != want {
				t.Errorf("CanTransitionTo(%s -> %s) = %v, 期望 %v", from, to, got, want)
			}
			err := from.MustTransitionTo(to)
			if want && err != nil {
				t.Errorf("MustTransitionTo(%s -> %s) 期望合法, 得到错误 %v", from, to, err)
			}
			if !want {
				if err == nil {
					t.Errorf("MustTransitionTo(%s -> %s) 期望错误, 得到 nil", from, to)
					continue
				}
				if !IsCode(err, ErrCodeInvalidTransition) {
					t.Errorf("MustTransitionTo(%s -> %s) 错误码 = %v, 期望 invalid_transition", from, to, AsDomain(err).Code)
				}
			}
		}
	}
}

func TestLotStatus_IsTerminal(t *testing.T) {
	for _, s := range AllLotStatuses() {
		want := s == LotStatusAccepted
		if got := s.IsTerminal(); got != want {
			t.Errorf("IsTerminal(%s) = %v, 期望 %v", s, got, want)
		}
	}
}

func TestMaterialLot_Validate(t *testing.T) {
	valid := func() *MaterialLot {
		return &MaterialLot{
			LotNo: "L1", SupplierID: 1, HeatNo: "H1", Grade: "304",
			Quantity: 10, ReceivedAt: time.Now(),
		}
	}
	if err := valid().Validate(); err != nil {
		t.Fatalf("合法批次校验失败: %v", err)
	}

	cases := []struct {
		name  string
		mutate func(l *MaterialLot)
		field string
	}{
		{"空批次号", func(l *MaterialLot) { l.LotNo = "" }, "lot_no"},
		{"超长批次号", func(l *MaterialLot) { l.LotNo = strings.Repeat("x", 65) }, "lot_no"},
		{"无供方", func(l *MaterialLot) { l.SupplierID = 0 }, "supplier_id"},
		{"空炉批号", func(l *MaterialLot) { l.HeatNo = "" }, "heat_no"},
		{"空牌号", func(l *MaterialLot) { l.Grade = "" }, "grade"},
		{"数量为零", func(l *MaterialLot) { l.Quantity = 0 }, "quantity"},
		{"数量为负", func(l *MaterialLot) { l.Quantity = -1 }, "quantity"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := valid()
			tc.mutate(l)
			err := l.Validate()
			if err == nil {
				t.Fatalf("期望校验错误")
			}
			de := AsDomain(err)
			if de.Code != ErrCodeValidation {
				t.Fatalf("错误码 = %s, 期望 validation", de.Code)
			}
			if de.Details["field"] != tc.field {
				t.Fatalf("错误字段 = %s, 期望 %s", de.Details["field"], tc.field)
			}
		})
	}
}

func TestMaterialLot_FinalResult(t *testing.T) {
	l := &MaterialLot{}
	if l.FinalResult() != "" {
		t.Fatalf("空结论期望空字符串")
	}
	l.InitialResult = "fail"
	if l.FinalResult() != "fail" {
		t.Fatalf("期望初检结论 fail, 实际 %s", l.FinalResult())
	}
	l.RetestResult = "pass"
	if l.FinalResult() != "pass" {
		t.Fatalf("复验结论应覆盖初检, 期望 pass, 实际 %s", l.FinalResult())
	}
}
