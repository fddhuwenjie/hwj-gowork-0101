package httpapi

import (
	"database/sql"

	"metalmics/internal/service"
)

// Handlers 聚合全部 HTTP 处理器所需的服务依赖。
type Handlers struct {
	daily     *service.DailyService
	exception *service.ExceptionService
	review    *service.ReviewService
	report    *service.ReportService
	jobs      *service.JobService
	db        *sql.DB
}

// NewHandlers 构造处理器集合。
func NewHandlers(
	daily *service.DailyService,
	exception *service.ExceptionService,
	review *service.ReviewService,
	report *service.ReportService,
	jobs *service.JobService,
	db *sql.DB,
) *Handlers {
	return &Handlers{
		daily:     daily,
		exception: exception,
		review:    review,
		report:    report,
		jobs:      jobs,
		db:        db,
	}
}
