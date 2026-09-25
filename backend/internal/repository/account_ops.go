package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

type accountOpsRepository struct{ db *sql.DB }

func NewAccountOpsRepository(db *sql.DB) service.AccountOpsRepository {
	return &accountOpsRepository{db: db}
}
func (r *accountOpsRepository) Record(ctx context.Context, e service.AccountOpsEvent) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO account_ops_alerts(account_id,kind,account_name,signal,http_status)
 VALUES($1,$2,$3,$4,$5) ON CONFLICT(account_id,kind) DO UPDATE SET
 account_name=EXCLUDED.account_name,signal=EXCLUDED.signal,http_status=EXCLUDED.http_status,last_seen=NOW(),occurrences=account_ops_alerts.occurrences+1,
 state=CASE WHEN account_ops_alerts.state IN ('sent','suppressed','failed') AND account_ops_alerts.next_send_at<=NOW() AND (account_ops_alerts.state<>'failed' OR account_ops_alerts.attempts>=3) THEN 'pending' ELSE account_ops_alerts.state END,
 attempts=CASE WHEN account_ops_alerts.state IN ('sent','suppressed','failed') AND account_ops_alerts.next_send_at<=NOW() AND (account_ops_alerts.state<>'failed' OR account_ops_alerts.attempts>=3) THEN 0 ELSE account_ops_alerts.attempts END`, e.AccountID, e.Kind, e.AccountName, e.Signal, e.HTTPStatus)
	return err
}

const accountOpsColumns = `account_id,kind,account_name,signal,http_status,first_seen,last_seen,occurrences,state,last_sent_at,next_send_at,attempts,lease`

func scanAccountOps(row scannable) (*service.AccountOpsEvent, error) {
	var e service.AccountOpsEvent
	err := row.Scan(&e.AccountID, &e.Kind, &e.AccountName, &e.Signal, &e.HTTPStatus, &e.FirstSeen, &e.LastSeen, &e.Occurrences, &e.State, &e.LastSentAt, &e.NextSendAt, &e.Attempts, &e.Lease)
	return &e, err
}
func (r *accountOpsRepository) Claim(ctx context.Context) (*service.AccountOpsEvent, error) {
	e, err := scanAccountOps(r.db.QueryRowContext(ctx, `UPDATE account_ops_alerts SET state='sending',attempts=attempts+1,lease=$1,lease_until=NOW()+INTERVAL '2 minutes'
 WHERE (account_id,kind)=(SELECT account_id,kind FROM account_ops_alerts WHERE
 ((state IN ('pending','failed') AND attempts<3 AND next_send_at<=NOW()) OR (state='sending' AND lease_until<NOW()))
 ORDER BY next_send_at LIMIT 1 FOR UPDATE SKIP LOCKED) RETURNING `+accountOpsColumns, uuid.NewString()))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return e, err
}
func (r *accountOpsRepository) Complete(ctx context.Context, e *service.AccountOpsEvent, state string, delay time.Duration) error {
	_, err := r.db.ExecContext(ctx, `UPDATE account_ops_alerts SET state=$4,lease='',lease_until=NULL,next_send_at=NOW()+($5*INTERVAL '1 second'),last_sent_at=CASE WHEN $4='sent' THEN NOW() ELSE last_sent_at END
 WHERE account_id=$1 AND kind=$2 AND lease=$3`, e.AccountID, e.Kind, e.Lease, state, int64(delay/time.Second))
	return err
}
func (r *accountOpsRepository) SuppressDisabled(ctx context.Context, c service.AccountOpsConfig) error {
	_, err := r.db.ExecContext(ctx, `UPDATE account_ops_alerts SET state='suppressed',lease='',lease_until=NULL
 WHERE state IN ('pending','failed') AND (NOT $1 OR (kind='balance_low' AND NOT $2) OR (kind='weekly_quota' AND NOT $3))`, c.Enabled, c.BalanceLow, c.WeeklyQuota)
	return err
}
func (r *accountOpsRepository) List(ctx context.Context, offset, limit int) ([]service.AccountOpsEvent, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+accountOpsColumns+` FROM account_ops_alerts ORDER BY last_seen DESC,account_id,kind LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.AccountOpsEvent, 0)
	for rows.Next() {
		e, err := scanAccountOps(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *e)
	}
	return items, rows.Err()
}
