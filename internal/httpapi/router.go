package httpapi

import (
	"log/slog"
	"net/http"
	"strings"
)

type legacyRoute struct {
	method   string
	segments []string
	handler  http.Handler
}

type legacyRouter struct {
	routes []legacyRoute
}

func (m *legacyRouter) Handle(method, pattern string, handler http.HandlerFunc) {
	m.routes = append(m.routes, legacyRoute{
		method:   method,
		segments: pathSegments(pattern),
		handler:  handler,
	})
}

func (m *legacyRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestSegments := pathSegments(r.URL.Path)
	var selected *legacyRoute
	selectedScore := -1
	pathMatched := false
	for i := range m.routes {
		route := &m.routes[i]
		matched, score := matchSegments(route.segments, requestSegments)
		if !matched {
			continue
		}
		pathMatched = true
		if route.method == r.Method && score > selectedScore {
			selected = route
			selectedScore = score
		}
	}
	if selected != nil {
		selected.handler.ServeHTTP(w, r)
		return
	}
	if pathMatched {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	http.NotFound(w, r)
}

func pathSegments(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func matchSegments(pattern, request []string) (bool, int) {
	if len(pattern) != len(request) {
		return false, 0
	}
	score := 0
	for i := range pattern {
		if strings.HasPrefix(pattern[i], "{") && strings.HasSuffix(pattern[i], "}") {
			continue
		}
		if pattern[i] != request[i] {
			return false, 0
		}
		score++
	}
	return true, score
}

// NewRouter 注册全部 HTTP JSON 端点并挂载中间件。
func NewRouter(h *Handlers, logger *slog.Logger) http.Handler {
	mux := &legacyRouter{}

	// 健康检查
	mux.Handle("GET", "/healthz", h.Healthz)

	// 供方
	mux.Handle("POST", "/api/v1/suppliers", h.CreateSupplier)
	mux.Handle("GET", "/api/v1/suppliers", h.ListSuppliers)
	mux.Handle("GET", "/api/v1/suppliers/{id}", h.GetSupplier)

	// 牌号规则版本
	mux.Handle("POST", "/api/v1/grade-rules", h.CreateGradeRule)
	mux.Handle("GET", "/api/v1/grade-rules", h.ListGradeRules)
	mux.Handle("POST", "/api/v1/grade-rules/{id}/activate", h.ActivateGradeRule)
	mux.Handle("POST", "/api/v1/grade-rules/{id}/retire", h.RetireGradeRule)

	// 来料批次与主流程
	mux.Handle("POST", "/api/v1/lots", h.RegisterLot)
	mux.Handle("GET", "/api/v1/lots", h.ListLots)
	mux.Handle("GET", "/api/v1/lots/{id}", h.GetLot)
	mux.Handle("POST", "/api/v1/lots/{id}/sampling-plans", h.CreateSamplingPlan)
	mux.Handle("POST", "/api/v1/sampling-plans/{id}/samples", h.RegisterSamples)
	mux.Handle("POST", "/api/v1/lots/{id}/sampling-complete", h.CompleteSampling)
	mux.Handle("POST", "/api/v1/lots/{id}/analyze", h.AnalyzeLot)
	mux.Handle("POST", "/api/v1/lots/{id}/judge", h.JudgeLot)
	mux.Handle("POST", "/api/v1/lots/{id}/accept", h.AcceptLot)
	mux.Handle("POST", "/api/v1/lots/{id}/reject", h.RejectLot)
	mux.Handle("POST", "/api/v1/lots/batch-accept", h.BatchAccept)
	mux.Handle("GET", "/api/v1/lots/{id}/conclusions", h.ListLotConclusions)
	mux.Handle("GET", "/api/v1/lots/{id}/detail", h.GetLotDetail)

	// 光谱分析
	mux.Handle("POST", "/api/v1/samples/{id}/spectrum-reports", h.SubmitSpectrumReport)
	mux.Handle("GET", "/api/v1/samples/{id}/spectrum-reports", h.ListSpectrumBySample)

	// 材质证明
	mux.Handle("POST", "/api/v1/lots/{id}/certificates", h.RegisterCertificate)
	mux.Handle("GET", "/api/v1/lots/{id}/certificates", h.ListCertificates)
	mux.Handle("POST", "/api/v1/certificates/{id}/verify", h.VerifyCertificate)

	// 异议复验
	mux.Handle("POST", "/api/v1/lots/{id}/retests", h.RequestRetest)
	mux.Handle("GET", "/api/v1/retests", h.ListRetests)
	mux.Handle("POST", "/api/v1/retests/{id}/approve", h.ApproveRetest)
	mux.Handle("POST", "/api/v1/retests/{id}/reject", h.RejectRetest)
	mux.Handle("POST", "/api/v1/retests/{id}/conclude", h.ConcludeRetest)

	// 异常处置（让步接收 / 隔离处置）
	mux.Handle("POST", "/api/v1/lots/{id}/dispositions", h.ProposeDisposition)
	mux.Handle("GET", "/api/v1/dispositions", h.ListDispositions)
	mux.Handle("POST", "/api/v1/dispositions/{id}/approve", h.ApproveDisposition)
	mux.Handle("POST", "/api/v1/dispositions/{id}/reject", h.RejectDisposition)
	mux.Handle("POST", "/api/v1/dispositions/{id}/execute", h.ExecuteDisposition)

	// 派生查询
	mux.Handle("GET", "/api/v1/reports/retest-accepted", h.ListRetestAccepted)
	mux.Handle("GET", "/api/v1/reports/cert-missing-accepted", h.CountCertMissingAccepted)

	// 审计
	mux.Handle("GET", "/api/v1/audit-events", h.ListAuditEvents)

	// 后台任务
	mux.Handle("POST", "/api/v1/jobs", h.EnqueueJob)
	mux.Handle("GET", "/api/v1/jobs", h.ListJobs)
	mux.Handle("POST", "/api/v1/jobs/{id}/retry", h.RetryJob)

	var handler http.Handler = mux
	handler = loggingMiddleware(logger)(handler)
	handler = recoverMiddleware(handler)
	return handler
}

// Healthz GET /healthz — 检查进程与数据库可用性。
func (h *Handlers) Healthz(w http.ResponseWriter, r *http.Request) {
	if err := h.db.PingContext(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, errorBody{Error: errorDetail{
			Code:    "unavailable",
			Message: "数据库不可用",
		}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
