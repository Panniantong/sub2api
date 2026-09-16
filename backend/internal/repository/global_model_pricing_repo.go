package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// globalModelPricingRepository 实现 service.GlobalModelPricingRepository。
// 直接 SQL 访问 global_model_pricing 表（无需 ent codegen）。
type globalModelPricingRepository struct {
	sql sqlExecutor
}

func NewGlobalModelPricingRepository(sqlDB *sql.DB) service.GlobalModelPricingRepository {
	return &globalModelPricingRepository{sql: sqlDB}
}

const globalModelPricingColumns = `id, model_pattern, billing_mode, input_price, output_price, cache_write_price, cache_write_1h_price, cache_read_price, per_request_price, enabled, created_at, updated_at`

func scanGlobalModelPricingRow(scan func(dest ...any) error) (*service.GlobalModelPrice, error) {
	var p service.GlobalModelPrice
	var input, output, cw, cw1h, cr, pr sql.NullFloat64
	var created, updated sql.NullTime
	if err := scan(&p.ID, &p.ModelPattern, &p.BillingMode,
		&input, &output, &cw, &cw1h, &cr, &pr,
		&p.Enabled, &created, &updated); err != nil {
		return nil, err
	}
	if input.Valid {
		p.InputPrice = &input.Float64
	}
	if output.Valid {
		p.OutputPrice = &output.Float64
	}
	if cw.Valid {
		p.CacheWritePrice = &cw.Float64
	}
	if cw1h.Valid {
		p.CacheWrite1hPrice = &cw1h.Float64
	}
	if cr.Valid {
		p.CacheReadPrice = &cr.Float64
	}
	if pr.Valid {
		p.PerRequestPrice = &pr.Float64
	}
	if created.Valid {
		p.CreatedAt = created.Time
	}
	if updated.Valid {
		p.UpdatedAt = updated.Time
	}
	return &p, nil
}

// ListEnabled 返回全部启用的全局定价（service 层做快照缓存）。
func (r *globalModelPricingRepository) ListEnabled(ctx context.Context) ([]service.GlobalModelPrice, error) {
	rows, err := r.sql.QueryContext(ctx,
		`SELECT `+globalModelPricingColumns+` FROM global_model_pricing WHERE enabled = TRUE ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]service.GlobalModelPrice, 0)
	for rows.Next() {
		p, err := scanGlobalModelPricingRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// ListAll 返回全部（含禁用，供管理端列表）。
func (r *globalModelPricingRepository) ListAll(ctx context.Context) ([]service.GlobalModelPrice, error) {
	rows, err := r.sql.QueryContext(ctx,
		`SELECT `+globalModelPricingColumns+` FROM global_model_pricing ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]service.GlobalModelPrice, 0)
	for rows.Next() {
		p, err := scanGlobalModelPricingRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func nullFloat64(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func (r *globalModelPricingRepository) Create(ctx context.Context, in service.GlobalModelPriceInput) (*service.GlobalModelPrice, error) {
	pattern := strings.ToLower(strings.TrimSpace(in.ModelPattern))
	mode := strings.TrimSpace(string(in.BillingMode))
	if mode == "" {
		mode = string(service.BillingModeToken)
	}
	rows, err := r.sql.QueryContext(ctx, `
		INSERT INTO global_model_pricing
			(model_pattern, billing_mode, input_price, output_price, cache_write_price,
			 cache_write_1h_price, cache_read_price, per_request_price, enabled, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NOW(),NOW())
		RETURNING `+globalModelPricingColumns,
		pattern, mode,
		nullFloat64(in.InputPrice), nullFloat64(in.OutputPrice),
		nullFloat64(in.CacheWritePrice), nullFloat64(in.CacheWrite1hPrice),
		nullFloat64(in.CacheReadPrice), nullFloat64(in.PerRequestPrice),
		in.Enabled)
	if err != nil {
		if isUniqueConstraintViolation(err) {
			return nil, fmt.Errorf("create global model pricing %q: %w", pattern, service.ErrGlobalModelPricingDuplicate)
		}
		return nil, fmt.Errorf("create global model pricing: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, fmt.Errorf("create global model pricing: no row returned")
	}
	p, err := scanGlobalModelPricingRow(rows.Scan)
	if err != nil {
		return nil, err
	}
	return p, rows.Err()
}

func (r *globalModelPricingRepository) GetByID(ctx context.Context, id int64) (*service.GlobalModelPrice, error) {
	rows, err := r.sql.QueryContext(ctx,
		`SELECT `+globalModelPricingColumns+` FROM global_model_pricing WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	p, err := scanGlobalModelPricingRow(rows.Scan)
	if err != nil {
		return nil, err
	}
	return p, rows.Err()
}

func (r *globalModelPricingRepository) Update(ctx context.Context, id int64, in service.GlobalModelPriceInput) (*service.GlobalModelPrice, error) {
	pattern := strings.ToLower(strings.TrimSpace(in.ModelPattern))
	mode := strings.TrimSpace(string(in.BillingMode))
	if mode == "" {
		mode = string(service.BillingModeToken)
	}
	rows, err := r.sql.QueryContext(ctx, `
		UPDATE global_model_pricing SET
			model_pattern = $2, billing_mode = $3,
			input_price = $4, output_price = $5,
			cache_write_price = $6, cache_write_1h_price = $7, cache_read_price = $8,
			per_request_price = $9, enabled = $10, updated_at = NOW()
		WHERE id = $1
		RETURNING `+globalModelPricingColumns, id, pattern, mode,
		nullFloat64(in.InputPrice), nullFloat64(in.OutputPrice),
		nullFloat64(in.CacheWritePrice), nullFloat64(in.CacheWrite1hPrice),
		nullFloat64(in.CacheReadPrice), nullFloat64(in.PerRequestPrice),
		in.Enabled)
	if err != nil {
		if isUniqueConstraintViolation(err) {
			return nil, fmt.Errorf("update global model pricing %q: %w", pattern, service.ErrGlobalModelPricingDuplicate)
		}
		return nil, fmt.Errorf("update global model pricing: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	p, err := scanGlobalModelPricingRow(rows.Scan)
	if err != nil {
		return nil, err
	}
	return p, rows.Err()
}

func (r *globalModelPricingRepository) Delete(ctx context.Context, id int64) error {
	rows, err := r.sql.QueryContext(ctx, `DELETE FROM global_model_pricing WHERE id = $1 RETURNING id`, id)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return sql.ErrNoRows
	}
	return rows.Err()
}

func (r *globalModelPricingRepository) SetEnabled(ctx context.Context, id int64, enabled bool) error {
	rows, err := r.sql.QueryContext(ctx,
		`UPDATE global_model_pricing SET enabled = $2, updated_at = NOW() WHERE id = $1 RETURNING id`, id, enabled)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return sql.ErrNoRows
	}
	return rows.Err()
}
