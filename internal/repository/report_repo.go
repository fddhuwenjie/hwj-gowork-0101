package repository

import (
	"context"
	"time"

	"metalmics/internal/domain"
)

// ReportRepo 承载跨多实体的派生查询（报表）。
type ReportRepo struct {
	db DBTX
}

// NewReportRepo 构造报表仓库。
func NewReportRepo(db DBTX) *ReportRepo {
	return &ReportRepo{db: db}
}

// ListRetestAccepted 派生查询一：筛选“初检不符合但复验仍接收”的来料批次，
// 返回批次号与材质证明编号，按批次 id 升序稳定排列。
//
// 条件：批次状态为 accepted，存在 fail 的初检结论，存在 pass 的复验结论。
func (r *ReportRepo) ListRetestAccepted(ctx context.Context, p domain.PageRequest) (domain.Page[domain.RetestAcceptedRow], error) {
	base := `
		FROM material_lots l
		JOIN suppliers s ON s.id = l.supplier_id
		JOIN conformity_conclusions ci ON ci.lot_id = l.id AND ci.round = 'initial' AND ci.result = 'fail'
		JOIN conformity_conclusions cr ON cr.lot_id = l.id AND cr.round = 'retest' AND cr.result = 'pass'
		LEFT JOIN mill_certificates mc ON mc.id = (
			SELECT MAX(id) FROM mill_certificates WHERE lot_id = l.id
		)
		WHERE l.status = 'accepted'`
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*)`+base).Scan(&total); err != nil {
		return domain.Page[domain.RetestAcceptedRow]{}, err
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT l.id, l.lot_no, s.code, l.grade, l.heat_no,
		        COALESCE(mc.cert_no, ''), cr.decided_by, cr.co_decided_by, COALESCE(l.accepted_at, l.updated_at)`+base+`
		 ORDER BY l.id ASC LIMIT ? OFFSET ?`, p.PageSize, p.Offset())
	if err != nil {
		return domain.Page[domain.RetestAcceptedRow]{}, err
	}
	defer rows.Close()
	items := []domain.RetestAcceptedRow{}
	for rows.Next() {
		var row domain.RetestAcceptedRow
		var acceptedAt string
		if err := rows.Scan(&row.LotID, &row.LotNo, &row.SupplierCode, &row.Grade, &row.HeatNo,
			&row.CertNo, &row.DecidedBy, &row.CoDecidedBy, &acceptedAt); err != nil {
			return domain.Page[domain.RetestAcceptedRow]{}, err
		}
		t, err := dbToTime(acceptedAt)
		if err != nil {
			return domain.Page[domain.RetestAcceptedRow]{}, err
		}
		row.AcceptedAt = t
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		return domain.Page[domain.RetestAcceptedRow]{}, err
	}
	return domain.NewPage(items, total, p), nil
}

// CountCertMissingAccepted 派生查询二：统计各供方在 [since, until] 区间内
// “材质证明缺失而先接收”的批次数量，按数量降序、供方编码升序稳定排列。
// 区间两端均含（>= since AND <= until），以匹配“近 N 天内实际接收”的语义。
func (r *ReportRepo) CountCertMissingAccepted(ctx context.Context, since, until time.Time) ([]domain.CertMissingAcceptedRow, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT s.id, s.code, s.name, COUNT(l.id) AS lot_count
		 FROM suppliers s
		 JOIN material_lots l ON l.supplier_id = s.id
		 WHERE l.status = 'accepted'
		   AND l.accepted_at >= ?
		   AND l.accepted_at <= ?
		   AND NOT EXISTS (SELECT 1 FROM mill_certificates mc WHERE mc.lot_id = l.id)
		 GROUP BY s.id, s.code, s.name
		 ORDER BY lot_count DESC, s.code ASC`, timeToDB(since), timeToDB(until))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.CertMissingAcceptedRow{}
	for rows.Next() {
		var row domain.CertMissingAcceptedRow
		if err := rows.Scan(&row.SupplierID, &row.SupplierCode, &row.SupplierName, &row.LotCount); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// ListAcceptedWithoutCert 列出 [since, until] 区间内接收且缺少材质证明的批次 id，
// 供后台任务（材质证明缺失扫描）使用。区间两端均含，与 CountCertMissingAccepted 口径一致。
func (r *ReportRepo) ListAcceptedWithoutCert(ctx context.Context, since, until time.Time) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT l.id FROM material_lots l
		 WHERE l.status = 'accepted' AND l.accepted_at >= ? AND l.accepted_at <= ?
		   AND NOT EXISTS (SELECT 1 FROM mill_certificates mc WHERE mc.lot_id = l.id)
		 ORDER BY l.id ASC`, timeToDB(since), timeToDB(until))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
