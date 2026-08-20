package domain

import "testing"

func TestCheckReadingsInRange(t *testing.T) {
	ranges := []ElementRange{
		{Element: "Cr", Min: 17, Max: 19},
		{Element: "Ni", Min: 8, Max: 10},
		{Element: "C", Min: 0, Max: 0.08},
	}

	t.Run("全部在范围内", func(t *testing.T) {
		v := CheckReadingsInRange(ranges, []ElementReading{
			{Element: "Cr", Value: 18}, {Element: "Ni", Value: 9}, {Element: "C", Value: 0.05},
		})
		if len(v) != 0 {
			t.Fatalf("期望无偏差, 实际 %v", v)
		}
	})

	t.Run("边界值等于上下界视为在范围内", func(t *testing.T) {
		v := CheckReadingsInRange(ranges, []ElementReading{
			{Element: "Cr", Value: 17}, {Element: "Ni", Value: 10}, {Element: "C", Value: 0},
		})
		if len(v) != 0 {
			t.Fatalf("边界值应在范围内, 实际 %v", v)
		}
	})

	t.Run("below_min", func(t *testing.T) {
		v := CheckReadingsInRange(ranges, []ElementReading{
			{Element: "Cr", Value: 16.9}, {Element: "Ni", Value: 9}, {Element: "C", Value: 0.05},
		})
		if len(v) != 1 {
			t.Fatalf("期望 1 个偏差, 实际 %d", len(v))
		}
		if v[0].Element != "Cr" || v[0].Reason != "below_min" || v[0].Value != 16.9 {
			t.Fatalf("偏差内容不符: %+v", v[0])
		}
	})

	t.Run("above_max", func(t *testing.T) {
		v := CheckReadingsInRange(ranges, []ElementReading{
			{Element: "Cr", Value: 18}, {Element: "Ni", Value: 10.1}, {Element: "C", Value: 0.05},
		})
		if len(v) != 1 || v[0].Reason != "above_max" || v[0].Element != "Ni" {
			t.Fatalf("偏差内容不符: %+v", v)
		}
	})

	t.Run("missing_reading", func(t *testing.T) {
		v := CheckReadingsInRange(ranges, []ElementReading{
			{Element: "Cr", Value: 18}, {Element: "Ni", Value: 9},
		})
		if len(v) != 1 || v[0].Reason != "missing_reading" || v[0].Element != "C" {
			t.Fatalf("偏差内容不符: %+v", v)
		}
	})

	t.Run("区间外元素被忽略", func(t *testing.T) {
		v := CheckReadingsInRange(ranges, []ElementReading{
			{Element: "Cr", Value: 18}, {Element: "Ni", Value: 9}, {Element: "C", Value: 0.05},
			{Element: "Mo", Value: 99}, {Element: "Cu", Value: 50},
		})
		if len(v) != 0 {
			t.Fatalf("规则外元素不应产生偏差, 实际 %v", v)
		}
	})

	t.Run("多种偏差同时返回", func(t *testing.T) {
		v := CheckReadingsInRange(ranges, []ElementReading{
			{Element: "Cr", Value: 16},
		})
		if len(v) != 3 {
			t.Fatalf("期望 3 个偏差, 实际 %d: %v", len(v), v)
		}
		reasons := map[string]string{}
		for _, x := range v {
			reasons[x.Element] = x.Reason
		}
		if reasons["Cr"] != "below_min" || reasons["Ni"] != "missing_reading" || reasons["C"] != "missing_reading" {
			t.Fatalf("偏差原因不符: %v", reasons)
		}
	})
}

func TestValidateRanges(t *testing.T) {
	cases := []struct {
		name   string
		ranges []ElementRange
		ok     bool
	}{
		{"合法", []ElementRange{{Element: "Cr", Min: 17, Max: 19}}, true},
		{"空列表", nil, false},
		{"元素名为空", []ElementRange{{Element: "", Min: 1, Max: 2}}, false},
		{"元素重复", []ElementRange{{Element: "Cr", Min: 1, Max: 2}, {Element: "Cr", Min: 3, Max: 4}}, false},
		{"下界大于上界", []ElementRange{{Element: "Cr", Min: 19, Max: 17}}, false},
		{"下界为负", []ElementRange{{Element: "Cr", Min: -1, Max: 17}}, false},
		{"上界超100", []ElementRange{{Element: "Cr", Min: 1, Max: 100.1}}, false},
		{"上下界相等", []ElementRange{{Element: "Cr", Min: 5, Max: 5}}, true},
		{"上界恰为100", []ElementRange{{Element: "Fe", Min: 60, Max: 100}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRanges(tc.ranges)
			if tc.ok && err != nil {
				t.Fatalf("期望合法, 得到 %v", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatalf("期望校验错误")
				}
				if !IsCode(err, ErrCodeValidation) {
					t.Fatalf("错误码 = %v, 期望 validation", AsDomain(err).Code)
				}
			}
		})
	}
}

func TestValidateReadings(t *testing.T) {
	cases := []struct {
		name     string
		readings []ElementReading
		ok       bool
	}{
		{"合法", []ElementReading{{Element: "Cr", Value: 18}}, true},
		{"空列表", nil, false},
		{"元素名为空", []ElementReading{{Element: "", Value: 1}}, false},
		{"元素重复", []ElementReading{{Element: "Cr", Value: 1}, {Element: "Cr", Value: 2}}, false},
		{"值为负", []ElementReading{{Element: "Cr", Value: -0.1}}, false},
		{"值超100", []ElementReading{{Element: "Cr", Value: 100.1}}, false},
		{"值为0", []ElementReading{{Element: "C", Value: 0}}, true},
		{"值为100", []ElementReading{{Element: "Fe", Value: 100}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateReadings(tc.readings)
			if tc.ok && err != nil {
				t.Fatalf("期望合法, 得到 %v", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatalf("期望校验错误")
				}
				if !IsCode(err, ErrCodeValidation) {
					t.Fatalf("错误码 = %v, 期望 validation", AsDomain(err).Code)
				}
			}
		})
	}
}

func TestEncodeDecodeRangesRoundTrip(t *testing.T) {
	in := []ElementRange{{Element: "Cr", Min: 17, Max: 19}, {Element: "Ni", Min: 8, Max: 10}}
	s, err := EncodeRanges(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := DecodeRanges(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0] != in[0] || out[1] != in[1] {
		t.Fatalf("往返不一致: %v", out)
	}
	if _, err := DecodeRanges("{bad json"); err == nil {
		t.Fatalf("非法 JSON 应报错")
	}
}

func TestEncodeDecodeReadingsRoundTrip(t *testing.T) {
	in := []ElementReading{{Element: "Cr", Value: 18.5}}
	s, err := EncodeReadings(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := DecodeReadings(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0] != in[0] {
		t.Fatalf("往返不一致: %v", out)
	}
	if _, err := DecodeReadings("{bad json"); err == nil {
		t.Fatalf("非法 JSON 应报错")
	}
}
