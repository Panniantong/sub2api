package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// One statement snapshot, no TTL cache and no transaction held during I/O to
// the provider. Only required library fields leave PostgreSQL.
func (r *settingRepository) CookieDispatchState(ctx context.Context, email, host string) ([]service.OpenAICodexCookieLibraryEntry, map[string]time.Time, error) {
	rows, err := r.client.QueryContext(ctx, `SELECT
	COALESCE((SELECT jsonb_agg(jsonb_build_object('host',e->>'host','cookie',e->>'cookie','expires_at',e->>'expires_at','captured_at',e->>'captured_at','payload',jsonb_build_object('exp',e->'payload'->'exp')))
	FROM settings s CROSS JOIN LATERAL jsonb_array_elements(COALESCE(NULLIF(btrim(s.value), ''), '[]')::jsonb) e
	WHERE s.key=$1 AND ($3='' OR lower(rtrim(btrim(e->>'host'), '.'))=$3)), '[]'::jsonb),
	COALESCE((SELECT jsonb_object_agg(host,cooldown_until) FROM cookie_email_host_cooldowns
	WHERE email=$2 AND cooldown_until>now()), '{}'::jsonb)`, service.SettingKeyOpenAICodexCookieLibrary, email, host)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("cookie dispatch state missing")
	}
	var libraryRaw, cooldownRaw []byte
	if err := rows.Scan(&libraryRaw, &cooldownRaw); err != nil {
		return nil, nil, err
	}
	var entries []service.OpenAICodexCookieLibraryEntry
	var cooldowns map[string]time.Time
	if err := json.Unmarshal(libraryRaw, &entries); err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(cooldownRaw, &cooldowns); err != nil {
		return nil, nil, err
	}
	return entries, cooldowns, rows.Err()
}
