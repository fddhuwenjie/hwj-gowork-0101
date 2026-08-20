// Package httpapi 提供 HTTP JSON 接口层：路由、处理器、中间件、
// 统一错误响应、分页过滤排序解析与优雅关闭。
package httpapi

import (
	"encoding/json"
	"net/http"

	"metalmics/internal/domain"
)

// errorBody 是统一 JSON 错误响应体。
type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Rule    string            `json:"rule,omitempty"`
	Details map[string]string `json:"details,omitempty"`
}

// writeJSON 以指定状态码输出 JSON。
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// writeError 将任意错误映射为统一 JSON 错误响应。
func writeError(w http.ResponseWriter, err error) {
	de := domain.AsDomain(err)
	writeJSON(w, statusOf(de.Code), errorBody{Error: errorDetail{
		Code:    string(de.Code),
		Message: de.Message,
		Rule:    de.Rule,
		Details: de.Details,
	}})
}

// statusOf 将领域错误码映射为 HTTP 状态码。
func statusOf(code domain.ErrorCode) int {
	switch code {
	case domain.ErrCodeNotFound:
		return http.StatusNotFound
	case domain.ErrCodeValidation:
		return http.StatusBadRequest
	case domain.ErrCodeVersionConflict:
		return http.StatusConflict
	case domain.ErrCodeInvalidTransition:
		return http.StatusConflict
	case domain.ErrCodeRuleViolation:
		return http.StatusUnprocessableEntity
	case domain.ErrCodeDuplicate:
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// decodeJSON 解析请求体 JSON；空 body 视为参数缺失。
func decodeJSON(r *http.Request, v interface{}) error {
	if r.Body == nil {
		return domain.Validation("body", "请求体不能为空")
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return domain.Validation("body", "请求体 JSON 非法: "+err.Error())
	}
	return nil
}
