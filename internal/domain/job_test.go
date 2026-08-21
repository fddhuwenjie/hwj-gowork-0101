package domain

import (
	"errors"
	"fmt"
	"testing"
)

// TestIsPermanent 校验不可恢复错误的判断与错误链解包。
func TestIsPermanent(t *testing.T) {
	if IsPermanent(nil) {
		t.Fatal("nil 不应判定为不可恢复错误")
	}
	if IsPermanent(fmt.Errorf("普通瞬时错误")) {
		t.Fatal("普通错误不应判定为不可恢复")
	}

	base := fmt.Errorf("未知任务类型: foo")
	pe := NewPermanentError(base)
	if !IsPermanent(pe) {
		t.Fatal("PermanentError 应判定为不可恢复")
	}
	if pe.Error() != base.Error() {
		t.Fatalf("错误信息应透传: got %q want %q", pe.Error(), base.Error())
	}
	// Unwrap 让 errors.Is 沿调用链工作
	if !errors.Is(pe, base) {
		t.Fatal("errors.Is 应能透过 PermanentError 匹配被包装错误")
	}
	// 双层包装仍可识别
	if !IsPermanent(fmt.Errorf("外层: %w", pe)) {
		t.Fatal("包裹 PermanentError 的错误仍应判定为不可恢复")
	}
	// NewPermanentError(nil) 返回 nil
	if NewPermanentError(nil) != nil {
		t.Fatal("NewPermanentError(nil) 应返回 nil")
	}
}

// TestRetryExhausted 校验耗尽判定：attempts 达到 max_attempts 即应进入终态，
// 不再依赖 payload 内容，避免任务滞留 pending 永远无法被调度。
func TestRetryExhausted(t *testing.T) {
	cases := []struct {
		name                          string
		attempts, maxAttempts         int
		payload                       string
		want                          bool
	}{
		{"未达上限", 1, 3, `{"days":30}`, false},
		{"恰等于上限", 3, 3, `{"days":30}`, true},
		{"超过上限", 4, 3, `{"days":30}`, true},
		{"单次尝试耗尽", 1, 1, `{}`, true},
		// 旧实现曾因 payload 含 exhaust 标记使用 > 比较，导致 attempts==max 时
		// 误判为未耗尽而滞留 pending；这里回归覆盖该路径。
		{"exhaust标记恰等于上限", 2, 2, `{"case":"exhaust"}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := &BackgroundJob{Attempts: c.attempts, MaxAttempts: c.maxAttempts, Payload: c.payload}
			if got := j.RetryExhausted(); got != c.want {
				t.Fatalf("RetryExhausted=%v, want %v", got, c.want)
			}
		})
	}
}
