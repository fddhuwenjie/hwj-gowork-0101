package service

import (
	"context"
	"time"

	"metalmics/internal/domain"
	"metalmics/internal/repository"
)

// ReportService 承载跨实体的派生查询与审计检索（复核归档流程的读侧）。
type ReportService struct {
	store *Store
	now   func() time.Time
}

// NewReportService 构造报表服务，使用真实 UTC 时钟。
func NewReportService(store *Store) *ReportService {
	return &ReportService{store: store, now: func() time.Time { return time.Now().UTC() }}
}

// NewReportServiceWithClock 以可注入时钟构造报表服务，用于时间窗口边界测试。
func NewReportServiceWithClock(store *Store, now func() time.Time) *ReportService {
	return &ReportService{store: store, now: now}
}

// ListRetestAccepted 筛选“初检不符合但复验仍接收”的来料批次及材质证明编号。
func (s *ReportService) ListRetestAccepted(ctx context.Context, p domain.PageRequest) (domain.Page[domain.RetestAcceptedRow], error) {
	return repository.NewReportRepo(s.store.DB()).ListRetestAccepted(ctx, p)
}

// CountCertMissingAccepted 统计各供方近期（days 天内）材质证明缺失而先接收的批次数量。
// 结果按数量降序、供方编码升序稳定排列。
//
// 时间窗口为 [now-days, now]（含两端）：覆盖最近 days 天内「实际接收」（accepted_at）
// 的批次。accepted_at 记录的是接收时刻而非日历日，故窗口以「接收时刻落在近 days 天内」
// 为准，上界到 now 即可，无需延伸到未来；正好 days 天前接收的批次仍被纳入，更早的排除。
func (s *ReportService) CountCertMissingAccepted(ctx context.Context, days int) ([]domain.CertMissingAcceptedRow, error) {
	if days <= 0 || days > 365 {
		return nil, domain.Validation("days", "days 须在 1-365 之间")
	}
	now := s.now().UTC()
	since := now.Add(-time.Duration(days) * 24 * time.Hour)
	return repository.NewReportRepo(s.store.DB()).CountCertMissingAccepted(ctx, since, now)
}

// ListAuditEvents 分页检索审计事件。
func (s *ReportService) ListAuditEvents(ctx context.Context, f domain.AuditFilter, p domain.PageRequest) (domain.Page[domain.AuditEvent], error) {
	return repository.NewAuditRepo(s.store.DB()).List(ctx, f, p)
}
