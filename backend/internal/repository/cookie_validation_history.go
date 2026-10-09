package repository

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *settingRepository) AppendCookieValidation(ctx context.Context, item service.OpenAICookieAcquisitionLog) error {
	if item.AccountEmail == "" && item.AccountID > 0 {
		rows, err := r.client.QueryContext(ctx, `SELECT cookie_account_email(credentials,extra,name) FROM accounts WHERE id=$1`, item.AccountID)
		if err != nil {
			return err
		}
		if rows.Next() {
			err = rows.Scan(&item.AccountEmail)
		}
		readErr := rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if readErr != nil {
			return readErr
		}
	}
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	attempt := item.AttemptID
	if attempt == "" {
		attempt = item.ID
	}
	_, err = r.client.ExecContext(ctx, `INSERT INTO cookie_validation_history(id,account_id,host,attempt_id,created_at,payload,account_email) VALUES($1,$2,$3,$4,$5,$6::jsonb,$7) ON CONFLICT DO NOTHING`, item.ID, item.AccountID, item.Host, attempt, item.CreatedAt, string(data), item.AccountEmail)
	return err
}

func (r *settingRepository) CookieValidationPage(ctx context.Context, account int64, host string, page, size int) ([]service.OpenAICookieAcquisitionLog, int64, error) {
	return r.CookieValidationEmailPage(ctx, account, "", host, page, size)
}

func (r *settingRepository) CookieValidationEmailPage(ctx context.Context, account int64, email, host string, page, size int) ([]service.OpenAICookieAcquisitionLog, int64, error) {
	rows, err := r.client.QueryContext(ctx, `SELECT count(DISTINCT attempt_id) FROM cookie_validation_history WHERE ($1::bigint=0 OR account_id=$1 OR account_email=NULLIF((SELECT cookie_account_email(credentials,extra,name) FROM accounts WHERE id=$1),'')) AND ($2::text='' OR host=$2) AND ($3::text='' OR account_email=$3)`, account, host, email)
	if err != nil {
		return nil, 0, err
	}
	var total int64
	if rows.Next() {
		err = rows.Scan(&total)
	}
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	rows, err = r.client.QueryContext(ctx, `WITH selected AS (SELECT attempt_id,max(created_at) AS latest FROM cookie_validation_history WHERE ($1::bigint=0 OR account_id=$1 OR account_email=NULLIF((SELECT cookie_account_email(credentials,extra,name) FROM accounts WHERE id=$1),'')) AND ($2::text='' OR host=$2) AND ($5::text='' OR account_email=$5) GROUP BY attempt_id ORDER BY latest DESC,attempt_id LIMIT $3 OFFSET $4) SELECT h.payload,h.account_email FROM cookie_validation_history h JOIN selected s USING(attempt_id) WHERE ($1::bigint=0 OR h.account_id=$1 OR h.account_email=NULLIF((SELECT cookie_account_email(credentials,extra,name) FROM accounts WHERE id=$1),'')) AND ($2::text='' OR h.host=$2) AND ($5::text='' OR h.account_email=$5) ORDER BY h.created_at DESC,h.id`, account, host, size, (page-1)*size, email)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []service.OpenAICookieAcquisitionLog{}
	for rows.Next() {
		var raw []byte
		var email string
		if err = rows.Scan(&raw, &email); err != nil {
			return nil, 0, err
		}
		var item service.OpenAICookieAcquisitionLog
		if err = json.Unmarshal(raw, &item); err != nil {
			return nil, 0, err
		}
		item.AccountEmail = email
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (r *settingRepository) CookieValidationHosts(ctx context.Context, account int64) ([]string, error) {
	rows, err := r.client.QueryContext(ctx, `SELECT DISTINCT host FROM cookie_validation_history WHERE (account_id=$1 OR account_email=NULLIF((SELECT cookie_account_email(credentials,extra,name) FROM accounts WHERE id=$1),'')) AND host<>'' ORDER BY host`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hosts := []string{}
	for rows.Next() {
		var host string
		if err = rows.Scan(&host); err != nil {
			return nil, err
		}
		hosts = append(hosts, host)
	}
	return hosts, rows.Err()
}
