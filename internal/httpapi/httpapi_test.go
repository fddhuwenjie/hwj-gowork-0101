package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"metalmics/internal/domain"
	"metalmics/internal/repository"
	"metalmics/internal/service"
)

// httpEnv 聚合 HTTP 测试所需的服务端与直连服务层。
type httpEnv struct {
	t      *testing.T
	server *httptest.Server
	db     *sql.DB
	daily  *service.DailyService
}

func newHTTPEnv(t *testing.T) *httpEnv {
	t.Helper()
	ctx := context.Background()
	db, err := repository.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	store := service.NewStore(db)
	daily := service.NewDailyService(store)
	handlers := NewHandlers(
		daily,
		service.NewExceptionService(store),
		service.NewReviewService(store),
		service.NewReportService(store),
		service.NewJobService(store),
		db,
	)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(NewRouter(handlers, logger))
	t.Cleanup(func() {
		server.Close()
		db.Close()
	})
	return &httpEnv{t: t, server: server, db: db, daily: daily}
}

// do 发起请求并解析 JSON 响应。
func (e *httpEnv) do(method, path string, body interface{}, headers map[string]string) (int, map[string]interface{}) {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.server.URL+path, reader)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	var parsed map[string]interface{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &parsed); err != nil {
			e.t.Fatalf("响应非 JSON: %s", raw)
		}
	}
	return resp.StatusCode, parsed
}

func (e *httpEnv) post(path string, body interface{}, actor string) (int, map[string]interface{}) {
	headers := map[string]string{}
	if actor != "" {
		headers["X-Actor"] = actor
	}
	return e.do(http.MethodPost, path, body, headers)
}

// errDetail 提取统一错误结构。
func errDetail(t *testing.T, resp map[string]interface{}) (code, message, rule string) {
	t.Helper()
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("缺少 error 字段: %v", resp)
	}
	code, _ = errObj["code"].(string)
	message, _ = errObj["message"].(string)
	rule, _ = errObj["rule"].(string)
	if code == "" || message == "" {
		t.Fatalf("错误结构缺少 code/message: %v", errObj)
	}
	return code, message, rule
}

// mustCreateSupplier 经 HTTP 创建供方。
func (e *httpEnv) mustCreateSupplier(code string) int64 {
	e.t.Helper()
	status, resp := e.post("/api/v1/suppliers", map[string]interface{}{"code": code, "name": "供方" + code}, "tester")
	if status != http.StatusCreated {
		e.t.Fatalf("创建供方失败: %d %v", status, resp)
	}
	data := resp["data"].(map[string]interface{})
	return int64(data["id"].(float64))
}

// mustActivateRule 经 HTTP 创建并激活规则。
func (e *httpEnv) mustActivateRule(grade string, versionNo int) {
	e.t.Helper()
	status, resp := e.post("/api/v1/grade-rules", map[string]interface{}{
		"grade": grade, "version_no": versionNo,
		"elements": []map[string]interface{}{{"element": "Cr", "min": 17, "max": 19}, {"element": "Ni", "min": 8, "max": 10}},
	}, "tester")
	if status != http.StatusCreated {
		e.t.Fatalf("创建规则失败: %d %v", status, resp)
	}
	id := int64(resp["data"].(map[string]interface{})["id"].(float64))
	status, resp = e.post(fmt.Sprintf("/api/v1/grade-rules/%d/activate", id), map[string]interface{}{"version": 1}, "tester")
	if status != http.StatusOK {
		e.t.Fatalf("激活规则失败: %d %v", status, resp)
	}
}

// mustRegisterLot 经 HTTP 登记批次，返回 id。
func (e *httpEnv) mustRegisterLot(lotNo string, supplierID int64, grade string) int64 {
	e.t.Helper()
	status, resp := e.post("/api/v1/lots", map[string]interface{}{
		"lot_no": lotNo, "supplier_id": supplierID, "heat_no": "H-" + lotNo,
		"grade": grade, "quantity": 10, "received_at": time.Now().UTC().Format(time.RFC3339),
	}, "tester")
	if status != http.StatusCreated {
		e.t.Fatalf("登记批次失败: %d %v", status, resp)
	}
	return int64(resp["data"].(map[string]interface{})["id"].(float64))
}

// mustJudgedLotID 经服务层快速构造 judged 批次，返回批次 id 与版本。
func (e *httpEnv) mustJudgedLotID(lotNo string, supplierID int64, pass bool) (int64, int64) {
	e.t.Helper()
	ctx := context.Background()
	lot := &domain.MaterialLot{
		LotNo: lotNo, SupplierID: supplierID, HeatNo: "H-" + lotNo, Grade: "304",
		Quantity: 10, ReceivedAt: time.Now().UTC(),
	}
	if _, err := e.daily.RegisterLot(ctx, lot, "tester"); err != nil {
		e.t.Fatal(err)
	}
	plan := &domain.SamplingPlan{PlanNo: "P-" + lotNo, LotID: lot.ID, RequiredCount: 1, RetainLocation: "柜A", CreatedBy: "tester"}
	if _, err := e.daily.CreateSamplingPlan(ctx, plan, "tester"); err != nil {
		e.t.Fatal(err)
	}
	smp := &domain.Sample{SampleNo: "S1", Retained: true}
	if _, err := e.daily.RegisterSamples(ctx, plan.ID, []*domain.Sample{smp}, "tester"); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.daily.CompleteSampling(ctx, lot.ID, 0, "tester"); err != nil {
		e.t.Fatal(err)
	}
	readings := []domain.ElementReading{{Element: "Cr", Value: 18}, {Element: "Ni", Value: 9}}
	if !pass {
		readings = []domain.ElementReading{{Element: "Cr", Value: 25}, {Element: "Ni", Value: 9}}
	}
	rep := &domain.SpectrumReport{ReportNo: "R-" + lotNo, SampleID: smp.ID, Readings: readings, Analyzer: "tester"}
	if _, err := e.daily.SubmitSpectrumReport(ctx, rep, "tester"); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.daily.AnalyzeLot(ctx, lot.ID, 0, "tester"); err != nil {
		e.t.Fatal(err)
	}
	cert := &domain.MillCertificate{CertNo: "C-" + lotNo, LotID: lot.ID, Grade: "304", HeatNo: lot.HeatNo, IssuedAt: time.Now().UTC()}
	if _, err := e.daily.RegisterCertificate(ctx, cert, "tester"); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.daily.VerifyCertificate(ctx, cert.ID, 0, "tester"); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.daily.JudgeLot(ctx, lot.ID, 0, "judge1", ""); err != nil {
		e.t.Fatal(err)
	}
	lot, err := e.daily.GetLot(ctx, lot.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return lot.ID, lot.Version
}

func TestHealthz(t *testing.T) {
	env := newHTTPEnv(t)
	status, resp := env.do(http.MethodGet, "/healthz", nil, nil)
	if status != http.StatusOK || resp["status"] != "ok" {
		t.Fatalf("healthz 不符: %d %v", status, resp)
	}
}

func TestCreateSupplier_201AndIdempotentReplay(t *testing.T) {
	env := newHTTPEnv(t)

	status, resp := env.post("/api/v1/suppliers", map[string]interface{}{"code": "S1", "name": "甲钢厂"}, "tester")
	if status != http.StatusCreated {
		t.Fatalf("首次创建应 201: %d %v", status, resp)
	}
	if resp["replayed"] != false {
		t.Fatalf("首次创建 replayed 应为 false: %v", resp)
	}
	id := resp["data"].(map[string]interface{})["id"].(float64)

	// 幂等重放：200 + replayed=true + 同一条数据
	status, resp = env.post("/api/v1/suppliers", map[string]interface{}{"code": "S1", "name": "甲钢厂"}, "tester")
	if status != http.StatusOK {
		t.Fatalf("重放应 200: %d %v", status, resp)
	}
	if resp["replayed"] != true {
		t.Fatalf("重放 replayed 应为 true: %v", resp)
	}
	if resp["data"].(map[string]interface{})["id"].(float64) != id {
		t.Fatalf("重放应返回既有记录: %v", resp)
	}
}

func TestErrorResponseStructure(t *testing.T) {
	env := newHTTPEnv(t)

	// 参数校验错误：400 + error.code/message
	status, resp := env.post("/api/v1/suppliers", map[string]interface{}{"name": "缺编码"}, "tester")
	if status != http.StatusBadRequest {
		t.Fatalf("期望 400: %d %v", status, resp)
	}
	code, _, _ := errDetail(t, resp)
	if code != "validation" {
		t.Fatalf("错误码 = %s, 期望 validation", code)
	}

	// 非法 JSON body：400
	req, _ := http.NewRequest(http.MethodPost, env.server.URL+"/api/v1/suppliers", bytes.NewBufferString("{oops"))
	req.Header.Set("Content-Type", "application/json")
	respRaw, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer respRaw.Body.Close()
	var parsed map[string]interface{}
	if err := json.NewDecoder(respRaw.Body).Decode(&parsed); err != nil {
		t.Fatal(err)
	}
	if respRaw.StatusCode != http.StatusBadRequest {
		t.Fatalf("非法 JSON 应 400: %d", respRaw.StatusCode)
	}
	code, _, _ = errDetail(t, parsed)
	if code != "validation" {
		t.Fatalf("错误码 = %s, 期望 validation", code)
	}

	// 资源不存在：404 not_found
	status, resp = env.do(http.MethodGet, "/api/v1/suppliers/99999", nil, nil)
	if status != http.StatusNotFound {
		t.Fatalf("期望 404: %d %v", status, resp)
	}
	code, _, _ = errDetail(t, resp)
	if code != "not_found" {
		t.Fatalf("错误码 = %s, 期望 not_found", code)
	}
}

func TestListPagination(t *testing.T) {
	env := newHTTPEnv(t)
	for _, c := range []string{"S1", "S2", "S3"} {
		env.mustCreateSupplier(c)
	}

	// page/page_size/sort/order
	status, resp := env.do(http.MethodGet, "/api/v1/suppliers?page=1&page_size=2&sort=code&order=desc", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("期望 200: %d %v", status, resp)
	}
	if resp["total"].(float64) != 3 || resp["page"].(float64) != 1 || resp["page_size"].(float64) != 2 {
		t.Fatalf("分页元数据不符: %v", resp)
	}
	items := resp["items"].([]interface{})
	if len(items) != 2 || items[0].(map[string]interface{})["code"] != "S3" || items[1].(map[string]interface{})["code"] != "S2" {
		t.Fatalf("排序不符: %v", items)
	}

	// 第二页
	status, resp = env.do(http.MethodGet, "/api/v1/suppliers?page=2&page_size=2&sort=code&order=desc", nil, nil)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	items = resp["items"].([]interface{})
	if len(items) != 1 || items[0].(map[string]interface{})["code"] != "S1" {
		t.Fatalf("第二页不符: %v", items)
	}

	// 非法 sort 返回 400
	status, resp = env.do(http.MethodGet, "/api/v1/suppliers?sort=bogus", nil, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("非法 sort 应 400: %d %v", status, resp)
	}
	code, _, _ := errDetail(t, resp)
	if code != "validation" {
		t.Fatalf("错误码 = %s, 期望 validation", code)
	}

	// 非法 order 返回 400
	status, _ = env.do(http.MethodGet, "/api/v1/suppliers?order=sideways", nil, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("非法 order 应 400: %d", status)
	}

	// 过滤
	status, resp = env.do(http.MethodGet, "/api/v1/suppliers?code_prefix=S", nil, nil)
	if status != http.StatusOK || resp["total"].(float64) != 3 {
		t.Fatalf("过滤不符: %d %v", status, resp)
	}
}

func TestRuleViolation_422(t *testing.T) {
	env := newHTTPEnv(t)
	supID := env.mustCreateSupplier("S1")
	// 不建规则：登记批次违反 R02
	status, resp := env.post("/api/v1/lots", map[string]interface{}{
		"lot_no": "L-422", "supplier_id": supID, "heat_no": "H1", "grade": "NOPE", "quantity": 1,
	}, "tester")
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("期望 422: %d %v", status, resp)
	}
	code, _, rule := errDetail(t, resp)
	if code != "rule_violation" || rule != domain.RuleActiveGradeRuleRequired {
		t.Fatalf("期望 R02 rule_violation: %v", resp)
	}
}

func TestInvalidTransition_409(t *testing.T) {
	env := newHTTPEnv(t)
	supID := env.mustCreateSupplier("S1")
	env.mustActivateRule("304", 1)
	lotID := env.mustRegisterLot("L-409", supID, "304")

	// registered 状态直接判定 -> invalid_transition -> 409
	status, resp := env.post(fmt.Sprintf("/api/v1/lots/%d/judge", lotID),
		map[string]interface{}{"version": 1, "decided_by": "judge1"}, "tester")
	if status != http.StatusConflict {
		t.Fatalf("期望 409: %d %v", status, resp)
	}
	code, _, _ := errDetail(t, resp)
	if code != "invalid_transition" {
		t.Fatalf("错误码 = %s, 期望 invalid_transition", code)
	}

	// 版本冲突也是 409
	env2 := newHTTPEnv(t)
	sup2 := env2.mustCreateSupplier("S2")
	env2.mustActivateRule("304", 1)
	lot2 := env2.mustRegisterLot("L-409B", sup2, "304")
	status, resp = env2.post(fmt.Sprintf("/api/v1/lots/%d/sampling-complete", lot2),
		map[string]interface{}{"version": 99}, "tester")
	if status != http.StatusConflict {
		t.Fatalf("版本冲突期望 409: %d %v", status, resp)
	}
	code, _, _ = errDetail(t, resp)
	if code != "version_conflict" {
		t.Fatalf("错误码 = %s, 期望 version_conflict", code)
	}
}

func TestConcurrentAccept_OnlyOnce(t *testing.T) {
	env := newHTTPEnv(t)
	supID := env.mustCreateSupplier("S1")
	env.mustActivateRule("304", 1)
	lotID, version := env.mustJudgedLotID("L-RACE", supID, true)

	const workers = 2
	statuses := make([]int, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			body, _ := json.Marshal(map[string]interface{}{"version": version})
			req, err := http.NewRequest(http.MethodPost,
				fmt.Sprintf("%s/api/v1/lots/%d/accept", env.server.URL, lotID), bytes.NewReader(body))
			if err != nil {
				t.Error(err)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Actor", "tester")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			defer resp.Body.Close()
			io.Copy(io.Discard, resp.Body)
			statuses[idx] = resp.StatusCode
		}(i)
	}
	close(start)
	wg.Wait()

	ok, conflict := 0, 0
	for _, s := range statuses {
		switch s {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
		default:
			t.Fatalf("非预期状态码: %d", s)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("并发接收应恰好一次成功: statuses=%v", statuses)
	}

	// 最终状态为 accepted
	status, resp := env.do(http.MethodGet, fmt.Sprintf("/api/v1/lots/%d", lotID), nil, nil)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	if resp["data"].(map[string]interface{})["status"] != "accepted" {
		t.Fatalf("最终状态不符: %v", resp)
	}
}

func TestRuleViolation_422_OnJudgeWithoutCert(t *testing.T) {
	env := newHTTPEnv(t)
	supID := env.mustCreateSupplier("S1")
	env.mustActivateRule("304", 1)

	// 走到 analyzed 但不登记证明，判定应 422 R04
	ctx := context.Background()
	lot := &domain.MaterialLot{
		LotNo: "L-422B", SupplierID: supID, HeatNo: "H1", Grade: "304",
		Quantity: 1, ReceivedAt: time.Now().UTC(),
	}
	if _, err := env.daily.RegisterLot(ctx, lot, "tester"); err != nil {
		t.Fatal(err)
	}
	plan := &domain.SamplingPlan{PlanNo: "P-422B", LotID: lot.ID, RequiredCount: 1, RetainLocation: "柜A", CreatedBy: "tester"}
	if _, err := env.daily.CreateSamplingPlan(ctx, plan, "tester"); err != nil {
		t.Fatal(err)
	}
	smp := &domain.Sample{SampleNo: "S1", Retained: true}
	if _, err := env.daily.RegisterSamples(ctx, plan.ID, []*domain.Sample{smp}, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.daily.CompleteSampling(ctx, lot.ID, 0, "tester"); err != nil {
		t.Fatal(err)
	}
	rep := &domain.SpectrumReport{ReportNo: "R-422B", SampleID: smp.ID,
		Readings: []domain.ElementReading{{Element: "Cr", Value: 18}, {Element: "Ni", Value: 9}}, Analyzer: "tester"}
	if _, err := env.daily.SubmitSpectrumReport(ctx, rep, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.daily.AnalyzeLot(ctx, lot.ID, 0, "tester"); err != nil {
		t.Fatal(err)
	}

	status, resp := env.post(fmt.Sprintf("/api/v1/lots/%d/judge", lot.ID),
		map[string]interface{}{"decided_by": "judge1"}, "tester")
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("期望 422: %d %v", status, resp)
	}
	code, _, rule := errDetail(t, resp)
	if code != "rule_violation" || rule != domain.RuleCertRequiredForJudgment {
		t.Fatalf("期望 R04 rule_violation: %v", resp)
	}
}
