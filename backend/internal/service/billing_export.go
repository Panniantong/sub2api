package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"strconv"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// billingExportMaxRows CSV 导出上限（防大结果集打爆内存/连接）。
const billingExportMaxRows = 10000

// BillingStatementRow 月账单单模型行。
type BillingStatementRow struct {
	Model        string  `json:"model"`
	Requests     int64   `json:"requests"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CacheTokens  int64   `json:"cache_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	Cost         float64 `json:"cost"`
}

// BillingStatement 月账单。
type BillingStatement struct {
	UserID   int64                 `json:"user_id"`
	Year     int                   `json:"year"`
	Month    int                   `json:"month"`
	Rows     []BillingStatementRow `json:"rows"`
	Requests int64                 `json:"requests"`
	Cost     float64               `json:"cost"`
}

// escapeCSVCell 防 CSV 公式注入：首字符为 = + - @ |（或空白/制表/换行开头）时
// 加单引号前缀，Excel 将其视为纯文本。
func escapeCSVCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '|', '\t', '\r', '\n', ' ':
		return "'" + s
	}
	return s
}

// BillingExportService 账单导出服务（SQL 直连）。
type BillingExportService struct {
	db *sql.DB
}

// NewBillingExportService 创建账单导出服务。
func NewBillingExportService(db *sql.DB) *BillingExportService {
	return &BillingExportService{db: db}
}

func parseBillingYearMonth(year, month int) (time.Time, time.Time, error) {
	if month < 1 || month > 12 {
		return time.Time{}, time.Time{}, infraerrors.BadRequest("BILLING_MONTH_INVALID", "month must be 1-12")
	}
	now := time.Now().UTC()
	if year < 2020 || year > now.Year()+1 {
		return time.Time{}, time.Time{}, infraerrors.BadRequest("BILLING_YEAR_INVALID", "year out of range")
	}
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, 0), nil
}

// Statement 按模型汇总月账单（收入口径：actual_cost 用户实扣）。
func (s *BillingExportService) Statement(ctx context.Context, userID int64, year, month int) (*BillingStatement, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("billing export service unavailable")
	}
	start, end, err := parseBillingYearMonth(year, month)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(NULLIF(requested_model, ''), model),
		  COUNT(*),
		  COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		  COALESCE(SUM(cache_read_tokens + cache_creation_tokens),0),
		  COALESCE(SUM(actual_cost),0)
		FROM usage_logs
		WHERE user_id = $1 AND created_at >= $2 AND created_at < $3
		GROUP BY 1 ORDER BY SUM(actual_cost) DESC`,
		userID, start, end)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	stmt := &BillingStatement{UserID: userID, Year: year, Month: month, Rows: []BillingStatementRow{}}
	for rows.Next() {
		var r BillingStatementRow
		var inTok, outTok, cacheTok int64
		if err := rows.Scan(&r.Model, &r.Requests, &inTok, &outTok, &cacheTok, &r.Cost); err != nil {
			return nil, err
		}
		r.InputTokens = inTok
		r.OutputTokens = outTok
		r.CacheTokens = cacheTok
		r.TotalTokens = inTok + outTok + cacheTok
		stmt.Rows = append(stmt.Rows, r)
		stmt.Requests += r.Requests
		stmt.Cost += r.Cost
	}
	return stmt, rows.Err()
}

// ExportCSV 导出用量明细 CSV（起止最多 31 天，上限 1 万行）。
func (s *BillingExportService) ExportCSV(ctx context.Context, userID int64, start, end time.Time) ([]byte, string, error) {
	if s == nil || s.db == nil {
		return nil, "", errors.New("billing export service unavailable")
	}
	if end.Before(start) || end.Sub(start) > 31*24*time.Hour {
		return nil, "", infraerrors.BadRequest("BILLING_RANGE_INVALID", "range must be within 31 days")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT created_at, COALESCE(NULLIF(requested_model, ''), model),
		  COALESCE(input_tokens,0), COALESCE(output_tokens,0),
		  COALESCE(cache_read_tokens + cache_creation_tokens,0),
		  COALESCE(actual_cost,0), COALESCE(request_id,'')
		FROM usage_logs
		WHERE user_id = $1 AND created_at >= $2 AND created_at < $3
		ORDER BY id DESC LIMIT $4`,
		userID, start.UTC(), end.UTC(), billingExportMaxRows)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rows.Close() }()
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"created_at", "model", "input_tokens", "output_tokens", "cache_tokens", "cost_usd", "request_id"})
	n := 0
	for rows.Next() {
		var ts time.Time
		var model string
		var inTok, outTok, cacheTok int64
		var cost float64
		var reqID string
		if err := rows.Scan(&ts, &model, &inTok, &outTok, &cacheTok, &cost, &reqID); err != nil {
			return nil, "", err
		}
		_ = w.Write([]string{
			ts.UTC().Format(time.RFC3339),
			escapeCSVCell(model),
			strconv.FormatInt(inTok, 10),
			strconv.FormatInt(outTok, 10),
			strconv.FormatInt(cacheTok, 10),
			strconv.FormatFloat(cost, 'f', 8, 64),
			escapeCSVCell(reqID),
		})
		n++
	}
	w.Flush()
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if err := w.Error(); err != nil {
		return nil, "", err
	}
	filename := fmt.Sprintf("billing-%d-%s.csv", userID, start.UTC().Format("20060102"))
	_ = n
	return buf.Bytes(), filename, nil
}
