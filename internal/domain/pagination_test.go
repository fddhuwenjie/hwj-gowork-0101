package domain

import "testing"

var testAllowed = map[string]string{"id": "id", "code": "code"}

func TestPageRequestNormalize(t *testing.T) {
	t.Run("零值全部取默认", func(t *testing.T) {
		p, err := PageRequest{}.Normalize("id", testAllowed)
		if err != nil {
			t.Fatal(err)
		}
		if p.Page != 1 || p.PageSize != 20 || p.Sort != "id" || p.Order != "asc" {
			t.Fatalf("默认值不符: %+v", p)
		}
	})

	t.Run("负页码与负页大小回退默认", func(t *testing.T) {
		p, err := PageRequest{Page: -3, PageSize: -5}.Normalize("id", testAllowed)
		if err != nil {
			t.Fatal(err)
		}
		if p.Page != 1 || p.PageSize != 20 {
			t.Fatalf("期望回退默认, 实际 %+v", p)
		}
	})

	t.Run("页大小超过上限被截断", func(t *testing.T) {
		p, err := PageRequest{Page: 2, PageSize: MaxPageSize + 1}.Normalize("id", testAllowed)
		if err != nil {
			t.Fatal(err)
		}
		if p.PageSize != MaxPageSize {
			t.Fatalf("期望截断到 %d, 实际 %d", MaxPageSize, p.PageSize)
		}
	})

	t.Run("合法排序与降序", func(t *testing.T) {
		p, err := PageRequest{Page: 1, PageSize: 10, Sort: "code", Order: "desc"}.Normalize("id", testAllowed)
		if err != nil {
			t.Fatal(err)
		}
		if p.Sort != "code" || p.Order != "desc" {
			t.Fatalf("排序参数不符: %+v", p)
		}
		if p.SortColumn(testAllowed) != "code" {
			t.Fatalf("SortColumn 不符")
		}
	})

	t.Run("空 order 归一为 asc", func(t *testing.T) {
		p, err := PageRequest{Sort: "id", Order: ""}.Normalize("id", testAllowed)
		if err != nil {
			t.Fatal(err)
		}
		if p.Order != "asc" {
			t.Fatalf("期望 asc, 实际 %s", p.Order)
		}
	})

	t.Run("非法排序字段报错", func(t *testing.T) {
		_, err := PageRequest{Sort: "password"}.Normalize("id", testAllowed)
		if err == nil {
			t.Fatal("期望错误")
		}
		if !IsCode(err, ErrCodeValidation) {
			t.Fatalf("错误码 = %v, 期望 validation", AsDomain(err).Code)
		}
	})

	t.Run("非法 order 报错", func(t *testing.T) {
		_, err := PageRequest{Sort: "id", Order: "sideways"}.Normalize("id", testAllowed)
		if err == nil {
			t.Fatal("期望错误")
		}
		if !IsCode(err, ErrCodeValidation) {
			t.Fatalf("错误码 = %v, 期望 validation", AsDomain(err).Code)
		}
	})

	t.Run("Offset 计算", func(t *testing.T) {
		p, _ := PageRequest{Page: 3, PageSize: 20}.Normalize("id", testAllowed)
		if p.Offset() != 40 {
			t.Fatalf("期望 40, 实际 %d", p.Offset())
		}
	})
}

func TestNewPage(t *testing.T) {
	p := NewPage[int](nil, 0, PageRequest{Page: 1, PageSize: 20})
	if p.Items == nil {
		t.Fatal("Items 不应为 nil")
	}
	if p.Total != 0 || p.Page != 1 || p.PageSize != 20 {
		t.Fatalf("分页字段不符: %+v", p)
	}
}
