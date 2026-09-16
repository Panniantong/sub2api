package repository

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const accountProtectionEnabledSQL = "COALESCE(extra -> 'anti_degradation' = 'true'::jsonb, extra #> '{anti_degrade,enabled}' = 'true'::jsonb, false)"

// An atomic expression preserves the committed state even when partial updates
// come from imports or asynchronous credential refreshes with stale snapshots.
func preserveProtectionExtraSQL(ctx context.Context, expression string) string {
	if service.ProtectionManagedWrite(ctx) {
		return expression
	}
	base := "ARRAY['anti_degradation','protection_scope','anti_degrade']::text[]"
	mode1 := "ARRAY['anti_degradation','protection_scope','anti_degrade','codex_fingerprint_mode','enable_tls_fingerprint','tls_fingerprint_builtin','tls_fingerprint_profile_id','proxy_mode']::text[]"
	modes := "'" + strings.Join(service.RegisteredProtectionModes(), "','") + "'"
	keys := "(CASE WHEN (COALESCE(extra #>> '{anti_degrade,enabled}', 'true') <> 'false' AND (extra #> '{anti_degrade,policy_version}' IS NOT NULL OR extra #>> '{anti_degrade,mode}' IN (" + modes + "))) THEN " + mode1 + " ELSE " + base + " END)"
	return "((" + expression + ") - " + keys + ") || COALESCE((SELECT jsonb_object_agg(key,value) FROM jsonb_each(COALESCE(extra,'{}'::jsonb)) WHERE key = ANY(" + keys + ")), '{}'::jsonb)"
}

func protectedConcurrencySQL(expression string) string {
	return "CASE WHEN " + accountProtectionEnabledSQL + " AND " + expression + " <= 0 THEN 16 ELSE " + expression + " END"
}

func preserveLockedAccountProtection(ctx context.Context, client *dbent.Client, a *service.Account) error {
	// lockAndMergeAccountProbeExtra already holds FOR NO KEY UPDATE in this
	// transaction. Re-read just extra under that lock to prevent stale writes.
	expected, compareVersion := service.GetProtectionWriteExpectation(ctx)
	query := "SELECT extra FROM accounts WHERE id = $1 AND deleted_at IS NULL"
	if compareVersion {
		query = "SELECT extra, updated_at FROM accounts WHERE id = $1 AND deleted_at IS NULL"
	}
	rows, err := client.QueryContext(ctx, query, a.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return service.ErrAccountNotFound
	}
	var raw []byte
	var revision time.Time
	var scanErr error
	if compareVersion {
		scanErr = rows.Scan(&raw, &revision)
	} else {
		scanErr = rows.Scan(&raw)
	}
	if scanErr != nil {
		return scanErr
	}
	if compareVersion && (expected.AccountID != a.ID || !expected.UpdatedAt.Equal(revision)) {
		return service.ErrProtectionConflict
	}
	var extra map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &extra); err != nil {
			return err
		}
	}
	current := &service.Account{Platform: a.Platform, Type: a.Type, Extra: extra}
	if !service.ProtectionManagedWrite(ctx) && service.ProtectedProxyModeConflict(current, a.Extra) {
		return service.ErrProtectedProxyModeChange
	}
	a.Extra = service.PreserveAccountProtection(ctx, current, a.Extra)
	service.BoundAccountProtectionConcurrency(a)
	if err := service.ValidateAccountProtectionConfiguration(a); err != nil {
		return err
	}
	return rows.Err()
}

func validateLockedBulkProxyMode(ctx context.Context, exec sqlExecutor, ids []int64, incoming map[string]any) error {
	rows, err := exec.QueryContext(ctx, `SELECT platform,type,extra FROM accounts WHERE id=ANY($1) AND deleted_at IS NULL ORDER BY id FOR NO KEY UPDATE`, pq.Array(ids))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a service.Account
		var raw []byte
		if err := rows.Scan(&a.Platform, &a.Type, &raw); err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &a.Extra); err != nil {
			return err
		}
		if service.ProtectedProxyModeConflict(&a, incoming) {
			return service.ErrProtectedProxyModeChange
		}
	}
	return rows.Err()
}
