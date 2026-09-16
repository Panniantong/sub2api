package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type intelligentTestRepository struct{ db *sql.DB }

func NewIntelligentTestRepository(db *sql.DB) service.IntelligentTestRepository {
	return &intelligentTestRepository{db: db}
}

const intelligentProtectionSQL = `COALESCE((a.extra->'anti_degradation')='true'::jsonb,(a.extra#>'{anti_degrade,enabled}')='true'::jsonb,false)`
const intelligentRecordColumns = `t.id,t.account_id,t.test_type,t.status,t.score,t.result,t.result_image,t.input,t.raw_response,t.raw_truncated,t.error_message,t.duration_ms,t.model,t.anti_degradation,t.config_snapshot,t.evaluation,t.started_at,t.finished_at,t.created_at,COALESCE(t.lease_token,''),t.queue_reason,t.available_at`
const intelligentSummaryColumns = `t.id,t.account_id,t.test_type,t.status,t.score,LEFT(t.result,1200),t.result_image,'','',t.raw_truncated,t.error_message,t.duration_ms,t.model,t.anti_degradation,'{}'::jsonb,t.evaluation,t.started_at,t.finished_at,t.created_at,'',t.queue_reason,t.available_at`

type intelligentScanner interface{ Scan(...any) error }

func scanIntelligentRecord(row intelligentScanner) (*service.IntelligentTestRecord, error) {
	r := &service.IntelligentTestRecord{}
	var cfg, eval []byte
	err := row.Scan(&r.ID, &r.AccountID, &r.TestType, &r.Status, &r.Score, &r.Result, &r.ResultImage, &r.Input, &r.RawResponse, &r.RawTruncated, &r.ErrorMessage, &r.DurationMS, &r.Model, &r.AntiDegradation, &cfg, &eval, &r.StartedAt, &r.FinishedAt, &r.CreatedAt, &r.LeaseToken, &r.QueueReason, &r.AvailableAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrIntelligentTestNotFound
	}
	if err != nil {
		return nil, err
	}
	r.ConfigSnapshot = &service.IntelligentTestConfig{}
	if err := json.Unmarshal(cfg, r.ConfigSnapshot); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(eval, &r.Evaluation); err != nil {
		return nil, err
	}
	return r, nil
}
func readIntelligentRecords(rows *sql.Rows) ([]*service.IntelligentTestRecord, error) {
	defer rows.Close()
	out := []*service.IntelligentTestRecord{}
	for rows.Next() {
		r, err := scanIntelligentRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func compactIntelligentRecord(r *service.IntelligentTestRecord) {
	r.Input = ""
	r.RawResponse = ""
	r.ConfigSnapshot = nil
	if len([]rune(r.Result)) > 600 {
		r.Result = string([]rune(r.Result)[:600]) + "…"
	}
}
func (r *intelligentTestRepository) IsAdmin(ctx context.Context, id int64) (bool, error) {
	var ok bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND role IN ('admin','super_admin') AND status='active' AND deleted_at IS NULL)`, id).Scan(&ok)
	return ok, err
}
func (r *intelligentTestRepository) Settings(ctx context.Context) ([]service.IntelligentTestSetting, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT test_type,enabled,user_visible,config,updated_at FROM test_settings ORDER BY test_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []service.IntelligentTestSetting{}
	for rows.Next() {
		var s service.IntelligentTestSetting
		var cfg []byte
		if err := rows.Scan(&s.TestType, &s.Enabled, &s.UserVisible, &cfg, &s.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(cfg, &s.Config); err != nil {
			return nil, err
		}
		s.Name = s.TestType
		switch s.TestType {
		case "pelican":
			s.Name = "鹈鹕测试"
		case "candy":
			s.Name = "糖果测试"
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
func insertIntelligentAudit(ctx context.Context, tx *sql.Tx, actor int64, action string, extra any) error {
	encoded, err := json.Marshal(extra)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_logs(actor_user_id,actor_email,actor_role,action,method,path,status_code,extra) SELECT id,email,role,$2,'POST','/admin/intelligent-tests',200,$3::jsonb FROM users WHERE id=$1`, actor, action, string(encoded))
	return err
}
func (r *intelligentTestRepository) UpdateSetting(ctx context.Context, actor int64, s *service.IntelligentTestSetting) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cfg, err := json.Marshal(s.Config)
	if err != nil {
		return err
	}
	var previous json.RawMessage
	err = tx.QueryRowContext(ctx, `SELECT jsonb_build_object('enabled',enabled,'user_visible',user_visible,'config',config) FROM test_settings WHERE test_type=$1 FOR UPDATE`, s.TestType).Scan(&previous)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrIntelligentTestNotFound
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE test_settings SET enabled=$2,user_visible=$3,config=$4,updated_at=NOW() WHERE test_type=$1`, s.TestType, s.Enabled, s.UserVisible, string(cfg))
	if err != nil {
		return err
	}
	if err = insertIntelligentAudit(ctx, tx, actor, "admin.intelligent_tests.settings", map[string]any{"test_type": s.TestType, "before": previous, "after": s}); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *intelligentTestRepository) Get(ctx context.Context, id int64) (*service.IntelligentTestRecord, error) {
	return scanIntelligentRecord(r.db.QueryRowContext(ctx, `SELECT `+intelligentRecordColumns+` FROM account_tests t WHERE t.id=$1`, id))
}

type intelligentWhere struct {
	parts []string
	args  []any
}

func (w *intelligentWhere) add(condition string, value any) {
	w.args = append(w.args, value)
	w.parts = append(w.parts, fmt.Sprintf(condition, len(w.args)))
}
func (w *intelligentWhere) sql() string { return strings.Join(w.parts, " AND ") }
func intelligentRecordWhere(f service.IntelligentTestFilter) *intelligentWhere {
	w := &intelligentWhere{parts: []string{"true"}}
	if f.AccountID > 0 {
		w.add("t.account_id=$%d", f.AccountID)
	}
	if f.TestType != "" {
		w.add("t.test_type=$%d", f.TestType)
	}
	if f.Status != "" {
		w.add("t.status=$%d", f.Status)
	}
	if f.From != nil {
		w.add("t.created_at >= $%d", *f.From)
	}
	if f.To != nil {
		w.add("t.created_at <= $%d", *f.To)
	}
	if f.OnlyAbnormal {
		w.parts = append(w.parts, `(t.status IN ('failed','rate_limited','account_error','model_error','request_error','network_error','suspected_degradation') OR t.evaluation->>'answer_verdict' IN ('incorrect','undetermined'))`)
	}
	return w
}
func (r *intelligentTestRepository) Records(ctx context.Context, f service.IntelligentTestFilter) (*service.IntelligentTestRecords, error) {
	out := &service.IntelligentTestRecords{Items: []*service.IntelligentTestRecord{}, Page: f.Page, PageSize: f.PageSize}
	w := intelligentRecordWhere(f)
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM account_tests t WHERE `+w.sql(), w.args...).Scan(&out.Total); err != nil {
		return nil, err
	}
	args := append(append([]any{}, w.args...), f.PageSize, (f.Page-1)*f.PageSize)
	rows, err := r.db.QueryContext(ctx, `SELECT `+intelligentSummaryColumns+` FROM account_tests t WHERE `+w.sql()+fmt.Sprintf(` ORDER BY t.id DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, err
	}
	out.Items, err = readIntelligentRecords(rows)
	for _, record := range out.Items {
		compactIntelligentRecord(record)
	}
	return out, err
}
func intelligentAccountWhere(f service.IntelligentTestFilter) *intelligentWhere {
	w := &intelligentWhere{parts: []string{"a.deleted_at IS NULL"}}
	if f.AccountID > 0 {
		w.add("a.id=$%d", f.AccountID)
	}
	if f.Search != "" {
		w.add("(a.name ILIKE $%[1]d OR COALESCE(a.notes,'') ILIKE $%[1]d OR a.id::text ILIKE $%[1]d)", "%"+f.Search+"%")
	}
	if f.Platform != "" {
		w.add("a.platform=$%d", f.Platform)
	}
	if f.AccountType != "" {
		w.add("a.type=$%d", f.AccountType)
	}
	if f.AccountStatus != "" {
		w.add("a.status=$%d", f.AccountStatus)
	}
	if f.GroupID > 0 {
		w.add("EXISTS(SELECT 1 FROM account_groups ag WHERE ag.account_id=a.id AND ag.group_id=$%d)", f.GroupID)
	}
	if f.AntiDegradation != nil {
		w.add(intelligentProtectionSQL+"=$%d", *f.AntiDegradation)
	}
	sub := `SELECT DISTINCT ON(t.test_type) t.test_type,t.status,t.evaluation FROM account_tests t WHERE t.account_id=a.id`
	if f.TestType != "" && (f.Status != "" || f.OnlyAbnormal) {
		w.args = append(w.args, f.TestType)
		sub += fmt.Sprintf(` AND t.test_type=$%d`, len(w.args))
	}
	completedSub := sub + ` AND t.status NOT IN ('queued','running','cancelled') ORDER BY t.test_type,t.id DESC`
	sub += ` ORDER BY t.test_type,t.id DESC`
	if f.Status == "waiting" {
		w.parts = append(w.parts, `NOT EXISTS(`+sub+`)`)
	} else if f.Status != "" {
		w.args = append(w.args, f.Status)
		w.parts = append(w.parts, `EXISTS(SELECT 1 FROM (`+sub+fmt.Sprintf(`) latest WHERE latest.status=$%d)`, len(w.args)))
	}
	if f.OnlyAbnormal {
		w.parts = append(w.parts, `EXISTS(SELECT 1 FROM (`+completedSub+`) latest WHERE latest.status IN ('failed','rate_limited','account_error','model_error','request_error','network_error','suspected_degradation') OR latest.evaluation->>'answer_verdict' IN ('incorrect','undetermined'))`)
	}
	return w
}
func (r *intelligentTestRepository) Accounts(ctx context.Context, f service.IntelligentTestFilter) (*service.IntelligentTestAccounts, error) {
	out := &service.IntelligentTestAccounts{Items: []service.IntelligentTestAccountCard{}, Page: f.Page, PageSize: f.PageSize}
	w := intelligentAccountWhere(f)
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts a WHERE `+w.sql(), w.args...).Scan(&out.Total); err != nil {
		return nil, err
	}
	out.Overview.TotalAccounts = out.Total
	overviewArgs := append([]any{}, w.args...)
	typeCondition := "true"
	if f.TestType != "" {
		overviewArgs = append(overviewArgs, f.TestType)
		typeCondition = fmt.Sprintf("t.test_type=$%d", len(overviewArgs))
	}
	overviewSQL := `WITH selected AS(SELECT a.id FROM accounts a WHERE ` + w.sql() + `), latest AS(
SELECT DISTINCT ON(t.account_id,t.test_type) t.account_id,t.test_type,t.status,t.evaluation FROM account_tests t JOIN selected a ON a.id=t.account_id WHERE ` + typeCondition + ` AND t.status NOT IN ('queued','running','cancelled') ORDER BY t.account_id,t.test_type,t.id DESC), per_account AS(
SELECT account_id,bool_or(COALESCE(evaluation->>'answer_verdict'='correct',false)) AS success,
bool_or(status IN ('failed','rate_limited','account_error','model_error','request_error','network_error')) AS abnormal,
bool_or(status='suspected_degradation' OR COALESCE(evaluation->>'answer_verdict' IN ('incorrect','undetermined'),false)) AS review
FROM latest GROUP BY account_id)
SELECT (SELECT COUNT(DISTINCT t.account_id) FROM account_tests t JOIN selected a ON a.id=t.account_id WHERE t.finished_at>=date_trunc('day',NOW()) AND t.status<>'cancelled' AND ` + typeCondition + `),COUNT(*) FILTER(WHERE success AND NOT abnormal AND NOT review),COUNT(*) FILTER(WHERE abnormal),COUNT(*) FILTER(WHERE review) FROM per_account`
	if err := r.db.QueryRowContext(ctx, overviewSQL, overviewArgs...).Scan(&out.Overview.TestedToday, &out.Overview.SuccessAccounts, &out.Overview.AbnormalAccounts, &out.Overview.ReviewAccounts); err != nil {
		return nil, err
	}
	args := append(append([]any{}, w.args...), f.PageSize, (f.Page-1)*f.PageSize)
	rows, err := r.db.QueryContext(ctx, `SELECT a.id,a.name,COALESCE(a.notes,''),a.platform,a.type,a.status,`+intelligentProtectionSQL+`,ARRAY(SELECT group_id FROM account_groups WHERE account_id=a.id ORDER BY group_id) FROM accounts a WHERE `+w.sql()+fmt.Sprintf(` ORDER BY a.id DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for rows.Next() {
		var card service.IntelligentTestAccountCard
		if err := rows.Scan(&card.AccountID, &card.Name, &card.Notes, &card.Platform, &card.AccountType, &card.AccountStatus, &card.AntiDegradation, pq.Array(&card.GroupIDs)); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, card.AccountID)
		if card.GroupIDs == nil {
			card.GroupIDs = []int64{}
		}
		card.Tests = []service.IntelligentTestCardTest{}
		out.Items = append(out.Items, card)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(ids) == 0 {
		return out, err
	}
	settings, err := r.Settings(ctx)
	if err != nil {
		return nil, err
	}
	latestRows, err := r.db.QueryContext(ctx, `SELECT `+intelligentSummaryColumns+` FROM account_tests t JOIN (SELECT DISTINCT ON(account_id,test_type) id FROM account_tests WHERE account_id=ANY($1) ORDER BY account_id,test_type,id DESC) latest ON latest.id=t.id`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	latest, err := readIntelligentRecords(latestRows)
	if err != nil {
		return nil, err
	}
	byKey := map[string]*service.IntelligentTestRecord{}
	key := func(id int64, t string) string { return fmt.Sprintf("%d:%s", id, t) }
	for _, record := range latest {
		compactIntelligentRecord(record)
		byKey[key(record.AccountID, record.TestType)] = record
	}
	completedRows, err := r.db.QueryContext(ctx, `SELECT `+intelligentSummaryColumns+` FROM account_tests t JOIN (SELECT DISTINCT ON(account_id,test_type) id FROM account_tests WHERE account_id=ANY($1) AND status NOT IN ('queued','running','cancelled') ORDER BY account_id,test_type,id DESC) latest ON latest.id=t.id`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	completed, err := readIntelligentRecords(completedRows)
	if err != nil {
		return nil, err
	}
	completedByKey := map[string]*service.IntelligentTestRecord{}
	for _, record := range completed {
		compactIntelligentRecord(record)
		completedByKey[key(record.AccountID, record.TestType)] = record
	}
	counts := map[string]int64{}
	countRows, err := r.db.QueryContext(ctx, `SELECT account_id,test_type,COUNT(*) FROM account_tests WHERE account_id=ANY($1) GROUP BY account_id,test_type`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	for countRows.Next() {
		var id, count int64
		var kind string
		if err := countRows.Scan(&id, &kind, &count); err != nil {
			countRows.Close()
			return nil, err
		}
		counts[key(id, kind)] = count
	}
	err = countRows.Err()
	countRows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out.Items {
		card := &out.Items[i]
		for _, setting := range settings {
			if f.TestType != "" && setting.TestType != f.TestType {
				continue
			}
			k := key(card.AccountID, setting.TestType)
			item := service.IntelligentTestCardTest{TestType: setting.TestType, Latest: byKey[k], LatestCompleted: completedByKey[k], HistoryCount: counts[k]}
			card.Tests = append(card.Tests, item)
		}
	}
	return out, nil
}
