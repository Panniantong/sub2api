package repository

import (
	"context"
	"time"
)

func (r *settingRepository) CookieEmailCooldowns(ctx context.Context, email string) (map[string]time.Time, error) {
	rows, err := r.client.QueryContext(ctx, `SELECT host,cooldown_until FROM cookie_email_host_cooldowns WHERE email=$1 AND cooldown_until>now()`, email)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]time.Time{}
	for rows.Next() {
		var host string
		var until time.Time
		if err := rows.Scan(&host, &until); err != nil {
			return nil, err
		}
		result[host] = until
	}
	return result, rows.Err()
}

func (r *settingRepository) SaveCookieEmailCooldown(ctx context.Context, email, host string, until time.Time) error {
	_, err := r.client.ExecContext(ctx, `INSERT INTO cookie_email_host_cooldowns(email,host,cooldown_until) VALUES($1,$2,$3)
	ON CONFLICT(email,host) DO UPDATE SET cooldown_until=GREATEST(cookie_email_host_cooldowns.cooldown_until,EXCLUDED.cooldown_until)`, email, host, until)
	return err
}
