package repository

import (
	"context"
	"database/sql"
	"fmt"

	"metalmics/internal/domain"
)

// ConclusionRepo 是符合性结论的持久化仓库。
type ConclusionRepo struct {
	db DBTX
}

// NewConclusionRepo 构造结论仓库。
func NewConclusionRepo(db DBTX) *ConclusionRepo {
	return &ConclusionRepo{db: db}
}

const conclusionColumns = `id, lot_id, round, result, cert_ok, spectrum_ok, reason,
	decided_by, co_decided_by, overrides_prev, created_at, version`

// Insert 插入结论；(lot_id, round) 冲突返回 Duplicate。
func (r *ConclusionRepo) Insert(ctx context.Context, c *domain.ConformityConclusion) error {
	c.CreatedAt = nowUTC()
	c.Version = 1
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO conformity_conclusions (lot_id, round, result, cert_ok, spectrum_ok, reason,
			decided_by, co_decided_by, overrides_prev, created_at, version)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.LotID, string(c.Round), string(c.Result), boolToInt(c.CertOK), boolToInt(c.SpectrumOK),
		c.Reason, c.DecidedBy, c.CoDecidedBy, boolToInt(c.OverridesPrev),
		timeToDB(c.CreatedAt), c.Version)
	if err != nil {
		if IsUniqueViolation(err) {
			return domain.Duplicate("conformity_conclusion", fmt.Sprintf("%d/%s", c.LotID, c.Round))
		}
		return fmt.Errorf("插入符合性结论失败: %w", err)
	}
	c.ID, err = res.LastInsertId()
	return err
}

// GetByLotRound 查询批次某轮结论，无则返回 nil, nil。
func (r *ConclusionRepo) GetByLotRound(ctx context.Context, lotID int64, round domain.ConclusionRound) (*domain.ConformityConclusion, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+conclusionColumns+` FROM conformity_conclusions WHERE lot_id = ? AND round = ?`,
		lotID, string(round))
	return scanConclusion(row)
}

// ListByLot 查询批次全部结论，按轮次（初检在前）稳定排列。
func (r *ConclusionRepo) ListByLot(ctx context.Context, lotID int64) ([]domain.ConformityConclusion, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+conclusionColumns+` FROM conformity_conclusions WHERE lot_id = ? ORDER BY id ASC`, lotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.ConformityConclusion{}
	for rows.Next() {
		c, err := scanConclusionRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *c)
	}
	return items, rows.Err()
}

func scanConclusion(row *sql.Row) (*domain.ConformityConclusion, error) {
	var c domain.ConformityConclusion
	var certOK, spectrumOK, overrides int
	var created string
	err := row.Scan(&c.ID, &c.LotID, &c.Round, &c.Result, &certOK, &spectrumOK, &c.Reason,
		&c.DecidedBy, &c.CoDecidedBy, &overrides, &created, &c.Version)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	c.CertOK, c.SpectrumOK, c.OverridesPrev = certOK == 1, spectrumOK == 1, overrides == 1
	t, err := dbToTime(created)
	if err != nil {
		return nil, err
	}
	c.CreatedAt = t
	return &c, nil
}

func scanConclusionRow(rows *sql.Rows) (*domain.ConformityConclusion, error) {
	var c domain.ConformityConclusion
	var certOK, spectrumOK, overrides int
	var created string
	if err := rows.Scan(&c.ID, &c.LotID, &c.Round, &c.Result, &certOK, &spectrumOK, &c.Reason,
		&c.DecidedBy, &c.CoDecidedBy, &overrides, &created, &c.Version); err != nil {
		return nil, err
	}
	c.CertOK, c.SpectrumOK, c.OverridesPrev = certOK == 1, spectrumOK == 1, overrides == 1
	t, err := dbToTime(created)
	if err != nil {
		return nil, err
	}
	c.CreatedAt = t
	return &c, nil
}
