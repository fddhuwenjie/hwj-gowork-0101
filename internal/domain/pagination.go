package domain

// PageRequest 描述分页、过滤之外的排序请求。
// Sort 为逻辑字段名，必须落在各列表端点声明的白名单内。
type PageRequest struct {
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
	Sort     string `json:"sort"`
	Order    string `json:"order"`
}

// MaxPageSize 是单页允许的最大条数，防止全表拉取。
const MaxPageSize = 100

// Normalize 校验并规范化分页参数：
// 页码从 1 开始，页大小限制在 [1, MaxPageSize]，排序字段必须在白名单内。
// allowed 为 逻辑字段名 -> 数据库列名 的映射。
func (p PageRequest) Normalize(defaultSort string, allowed map[string]string) (PageRequest, error) {
	if p.Page <= 0 {
		p.Page = 1
	}
	if p.PageSize <= 0 {
		p.PageSize = 20
	}
	if p.PageSize > MaxPageSize {
		p.PageSize = MaxPageSize
	}
	if p.Sort == "" {
		p.Sort = defaultSort
	}
	if _, ok := allowed[p.Sort]; !ok {
		return p, Validation("sort", "不支持的排序字段: "+p.Sort)
	}
	switch p.Order {
	case "", "asc":
		p.Order = "asc"
	case "desc":
	default:
		return p, Validation("order", "order 仅支持 asc/desc")
	}
	return p, nil
}

// SortColumn 返回排序字段对应的数据库列名。
func (p PageRequest) SortColumn(allowed map[string]string) string {
	return allowed[p.Sort]
}

// Offset 计算 SQL OFFSET。
func (p PageRequest) Offset() int {
	return (p.Page - 1) * p.PageSize
}

// Page 是分页结果，Items 顺序由稳定排序保证（排序键相同再按 id 升序）。
type Page[T any] struct {
	Items    []T   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

// NewPage 组装分页结果。
func NewPage[T any](items []T, total int64, req PageRequest) Page[T] {
	if items == nil {
		items = []T{}
	}
	return Page[T]{Items: items, Total: total, Page: req.Page, PageSize: req.PageSize}
}
