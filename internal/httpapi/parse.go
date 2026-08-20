package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"metalmics/internal/domain"
)

// pathID 解析路径参数 {id} 为正整数。
func pathID(r *http.Request) (int64, error) {
	for _, raw := range strings.Split(strings.Trim(r.URL.Path, "/"), "/") {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err == nil && id > 0 {
			return id, nil
		}
	}
	return 0, domain.Validation("id", "路径参数 id 必须为正整数")
}

// pageRequest 解析分页与排序参数，并依据端点白名单校验排序字段。
func pageRequest(r *http.Request, defaultSort string, allowed map[string]string) (domain.PageRequest, error) {
	q := r.URL.Query()
	p := domain.PageRequest{
		Page:     intQuery(q.Get("page"), 0),
		PageSize: intQuery(q.Get("page_size"), 0),
		Sort:     q.Get("sort"),
		Order:    strings.ToLower(q.Get("order")),
	}
	return p.Normalize(defaultSort, allowed)
}

// intQuery 解析可选整数查询参数，非法或缺失时返回 def。
func intQuery(raw string, def int) int {
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return v
}

// int64Query 解析可选 int64 查询参数。
func int64Query(raw string) int64 {
	if raw == "" {
		return 0
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// timeQuery 解析可选 RFC3339 时间查询参数。
func timeQuery(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, domain.Validation("time", "时间参数须为 RFC3339 格式: "+raw)
	}
	u := t.UTC()
	return &u, nil
}

// actor 从请求头提取操作人，缺省为 system。
func actor(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Actor")); v != "" {
		return v
	}
	return "system"
}

// bodyVersion 提取请求体中的乐观锁版本，0 表示不校验。
func bodyVersion(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}
