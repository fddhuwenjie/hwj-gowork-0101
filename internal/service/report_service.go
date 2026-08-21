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
}

// NewReportService 构造报表服务。
func NewReportService(store *Store) *ReportService {
	return &ReportService{store: store}
}

// ListRetestAccepted 筛选“初检不符合但复验仍接收”的来料批次及材质证明编号。
func (s *ReportService) ListRetestAccepted(ctx context.Context, p domain.PageRequest) (domain.Page[domain.RetestAcceptedRow], error) {
	return repository.NewReportRepo(s.store.DB()).ListRetestAccepted(ctx, p)
}

// CountCertMissingAccepted 统计各供方近期（days 天内）材质证明缺失而先接收的批次数量。
// 结果按数量降序、供方编码升序稳定排列。
func (s *ReportService) CountCertMissingAccepted(ctx context.Context, days int) ([]domain.CertMissingAcceptedRow, error) {
	if days <= 0 || days > 365 {
		return nil, domain.Validation("days", "days 须在 1-365 之间")
	}
	since := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	return repository.NewReportRepo(s.store.DB()).CountCertMissingAccepted(ctx, since)
}

// ListAuditEvents 分页检索审计事件。
func (s *ReportService) ListAuditEvents(ctx context.Context, f domain.AuditFilter, p domain.PageRequest) (domain.Page[domain.AuditEvent], error) {
	repo := repository.NewAuditRepo(s.store.DB())
	if f.Entity != "" && f.EntityID > 0 && f.Actor != "" {
		return repo.ListByActorEntityID(ctx, f.EntityID, f.Actor, p)
	}
	return repo.List(ctx, f, p)
}
