package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"metalmics/internal/domain"
)

// CertificateRepo 是材质证明的持久化仓库。
type CertificateRepo struct {
	db DBTX
}

// NewCertificateRepo 构造材质证明仓库。
func NewCertificateRepo(db DBTX) *CertificateRepo {
	return &CertificateRepo{db: db}
}

const certColumns = `id, cert_no, lot_id, grade, heat_no, elements, issued_at,
	verified, verified_by, verified_at, created_at, version`

// Insert 插入材质证明；cert_no 冲突返回 Duplicate 供幂等处理。
func (r *CertificateRepo) Insert(ctx context.Context, c *domain.MillCertificate) error {
	encoded, err := domain.EncodeRanges(c.Elements)
	if err != nil {
		return err
	}
	c.CreatedAt = nowUTC()
	c.Version = 1
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO mill_certificates (cert_no, lot_id, grade, heat_no, elements, issued_at,
			verified, verified_by, verified_at, created_at, version)
		 VALUES (?, ?, ?, ?, ?, ?, 0, '', NULL, ?, ?)`,
		c.CertNo, c.LotID, c.Grade, c.HeatNo, encoded, timeToDB(c.IssuedAt),
		timeToDB(c.CreatedAt), c.Version)
	if err != nil {
		if IsUniqueViolation(err) {
			return domain.Duplicate("mill_certificate", c.CertNo)
		}
		return fmt.Errorf("插入材质证明失败: %w", err)
	}
	c.ID, err = res.LastInsertId()
	return err
}

// GetByID 按主键查询材质证明。
func (r *CertificateRepo) GetByID(ctx context.Context, id int64) (*domain.MillCertificate, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+certColumns+` FROM mill_certificates WHERE id = ?`, id)
	return scanCert(row)
}

// GetByCertNo 按证明编号查询。
func (r *CertificateRepo) GetByCertNo(ctx context.Context, certNo string) (*domain.MillCertificate, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+certColumns+` FROM mill_certificates WHERE cert_no = ?`, certNo)
	return scanCert(row)
}

// LatestByLot 查询批次最新登记的材质证明，无则返回 nil, nil。
func (r *CertificateRepo) LatestByLot(ctx context.Context, lotID int64) (*domain.MillCertificate, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+certColumns+` FROM mill_certificates WHERE lot_id = ? ORDER BY id DESC LIMIT 1`, lotID)
	return scanCert(row)
}

// ListByLot 查询批次全部材质证明，按 id 升序稳定排列。
func (r *CertificateRepo) ListByLot(ctx context.Context, lotID int64) ([]domain.MillCertificate, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+certColumns+` FROM mill_certificates WHERE lot_id = ? ORDER BY id DESC`, lotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCertRows(rows)
}

func scanCert(row *sql.Row) (*domain.MillCertificate, error) {
	var c domain.MillCertificate
	var elements, issuedAt, created string
	var verifiedAt sql.NullString
	var verified int
	err := row.Scan(&c.ID, &c.CertNo, &c.LotID, &c.Grade, &c.HeatNo, &elements, &issuedAt,
		&verified, &c.VerifiedBy, &verifiedAt, &created, &c.Version)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if err := fillCertFields(&c, elements, issuedAt, verified, verifiedAt, created); err != nil {
		return nil, err
	}
	return &c, nil
}

func scanCertRows(rows *sql.Rows) ([]domain.MillCertificate, error) {
	items := []domain.MillCertificate{}
	for rows.Next() {
		var c domain.MillCertificate
		var elements, issuedAt, created string
		var verifiedAt sql.NullString
		var verified int
		if err := rows.Scan(&c.ID, &c.CertNo, &c.LotID, &c.Grade, &c.HeatNo, &elements, &issuedAt,
			&verified, &c.VerifiedBy, &verifiedAt, &created, &c.Version); err != nil {
			return nil, err
		}
		if err := fillCertFields(&c, elements, issuedAt, verified, verifiedAt, created); err != nil {
			return nil, err
		}
		items = append(items, c)
	}
	return items, rows.Err()
}

func fillCertFields(c *domain.MillCertificate, elements, issuedAt string, verified int, verifiedAt sql.NullString, created string) error {
	ranges, err := domain.DecodeRanges(elements)
	if err != nil {
		return err
	}
	c.Elements = ranges
	c.Verified = verified == 1
	if c.IssuedAt, err = dbToTime(issuedAt); err != nil {
		return err
	}
	if c.CreatedAt, err = dbToTime(created); err != nil {
		return err
	}
	t, err := nullTimeFromDB(verifiedAt)
	if err != nil {
		return err
	}
	c.VerifiedAt = t
	return nil
}

// MarkVerified 标记核对通过，带乐观锁校验。
func (r *CertificateRepo) MarkVerified(ctx context.Context, id int64, expectedVersion int64, verifiedBy string, verifiedAt time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE mill_certificates SET verified = 1, verified_by = ?, verified_at = ?, version = version + 1
		 WHERE id = ? AND version = ?`,
		verifiedBy, timeToDB(verifiedAt), id, expectedVersion)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
