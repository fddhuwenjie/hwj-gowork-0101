package domain

import "fmt"

// ErrorCode 是统一的业务错误码，HTTP 层据此映射状态码。
type ErrorCode string

const (
	ErrCodeNotFound          ErrorCode = "not_found"
	ErrCodeValidation        ErrorCode = "validation"
	ErrCodeVersionConflict   ErrorCode = "version_conflict"
	ErrCodeInvalidTransition ErrorCode = "invalid_transition"
	ErrCodeRuleViolation     ErrorCode = "rule_violation"
	ErrCodeDuplicate         ErrorCode = "duplicate"
	ErrCodeInternal          ErrorCode = "internal"
)

// Error 是领域层统一错误类型，携带错误码、可读信息与可选明细。
type Error struct {
	Code    ErrorCode         `json:"code"`
	Message string            `json:"message"`
	Rule    string            `json:"rule,omitempty"`
	Details map[string]string `json:"details,omitempty"`
}

// Error 实现 error 接口。
func (e *Error) Error() string {
	if e.Rule != "" {
		return fmt.Sprintf("%s: %s (rule=%s)", e.Code, e.Message, e.Rule)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// WithDetail 为错误补充一个明细字段并返回自身，便于链式构造。
func (e *Error) WithDetail(key, value string) *Error {
	if e.Details == nil {
		e.Details = make(map[string]string)
	}
	e.Details[key] = value
	return e
}

// NotFound 构造资源不存在错误。
func NotFound(entity string, id interface{}) *Error {
	return &Error{
		Code:    ErrCodeNotFound,
		Message: fmt.Sprintf("%s 不存在: %v", entity, id),
	}
}

// Validation 构造参数校验错误。
func Validation(field, message string) *Error {
	return &Error{
		Code:    ErrCodeValidation,
		Message: message,
		Details: map[string]string{"field": field},
	}
}

// VersionConflict 构造乐观锁版本冲突错误。
func VersionConflict(entity string, id int64, expected, actual int64) *Error {
	return &Error{
		Code:    ErrCodeVersionConflict,
		Message: fmt.Sprintf("%s(%d) 版本冲突: 期望 %d, 实际 %d", entity, id, expected, actual),
	}
}

// InvalidTransition 构造非法状态转换错误。
func InvalidTransition(entity string, from, to string) *Error {
	return &Error{
		Code:    ErrCodeInvalidTransition,
		Message: fmt.Sprintf("%s 不允许从状态 %s 转换到 %s", entity, from, to),
	}
}

// RuleViolation 构造跨实体业务规则违反错误。
func RuleViolation(rule, message string) *Error {
	return &Error{
		Code:    ErrCodeRuleViolation,
		Rule:    rule,
		Message: message,
	}
}

// Duplicate 构造唯一键冲突错误（用于幂等重放的识别）。
func Duplicate(entity, key string) *Error {
	return &Error{
		Code:    ErrCodeDuplicate,
		Message: fmt.Sprintf("%s 已存在相同标识: %s", entity, key),
	}
}

// IsCode 判断 err 是否为指定错误码的领域错误。
func IsCode(err error, code ErrorCode) bool {
	e, ok := err.(*Error)
	return ok && e.Code == code
}

// AsDomain 将任意 error 转换为领域错误；非领域错误包装为 internal。
func AsDomain(err error) *Error {
	if e, ok := err.(*Error); ok {
		return e
	}
	return &Error{Code: ErrCodeInternal, Message: err.Error()}
}
