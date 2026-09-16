package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

type UserCleanupAuditContext struct {
	AuthMethod string
	ClientIP   string
	RequestID  string
	Method     string
	Path       string
}

func (s *UserCleanupService) Execute(ctx context.Context, actorID int64, previewID string, confirmed bool, expectedCount int, audit UserCleanupAuditContext) (out *UserCleanupResult, err error) {
	defer func() {
		var pgErr *pq.Error
		if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
			err = ErrUserCleanupBusy
		}
	}()
	if !confirmed || expectedCount < 1 || expectedCount > UserCleanupBatchLimit {
		return nil, ErrUserCleanupConfirmation
	}
	if _, err := uuid.Parse(previewID); err != nil {
		return nil, ErrUserCleanupPreview
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// A concurrent retry can briefly wait for the same preview's persisted result.
	if _, err := tx.ExecContext(ctx, `SET LOCAL lock_timeout = '15s'`); err != nil {
		return nil, err
	}
	actor, err := cleanupActor(ctx, tx, actorID)
	if err != nil {
		return nil, err
	}
	var ids pq.Int64Array
	var cached []byte
	var valid bool
	err = tx.QueryRowContext(ctx, `SELECT candidate_ids,result,expires_at>NOW() FROM user_cleanup_previews WHERE id=$1 AND actor_user_id=$2 FOR UPDATE`, previewID, actorID).Scan(&ids, &cached, &valid)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserCleanupPreview
	}
	if err != nil {
		return nil, err
	}
	if len(ids) != expectedCount {
		return nil, ErrUserCleanupConfirmation
	}
	if len(cached) > 0 {
		var result UserCleanupResult
		if err := json.Unmarshal(cached, &result); err != nil {
			return nil, err
		}
		result.Replayed = true
		return &result, nil
	}
	if !valid {
		return nil, ErrUserCleanupPreview
	}
	// Financial/account row contention fails promptly and leaves the preview retryable.
	if _, err := tx.ExecContext(ctx, `SET LOCAL lock_timeout = '3s'`); err != nil {
		return nil, err
	}
	// This separate statement is essential: any balance/usage transaction already in
	// progress commits before the next READ COMMITTED eligibility snapshot is taken.
	// FOR UPDATE also conflicts with FK KEY SHARE locks taken by new usage_log rows.
	rows, err := tx.QueryContext(ctx, `SELECT id FROM users WHERE id=ANY($1) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	// API authentication touches last_used_at before forwarding, including long
	// requests whose usage log is not yet written. Lock those rows before rechecking.
	keysByUser, err := cleanupLockKeys(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	candidates, err := cleanupCandidates(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	result := &UserCleanupResult{PreviewID: previewID, UserIDs: make([]int64, 0, len(candidates)), SkippedCount: len(ids) - len(candidates)}
	for _, candidate := range candidates {
		result.UserIDs = append(result.UserIDs, candidate.ID)
		if candidate.ZeroBalance {
			result.ZeroBalance++
		} else {
			result.NegativeBalance++
		}
	}
	result.DeletedCount = len(result.UserIDs)
	var cacheKeys []string
	for _, id := range result.UserIDs {
		cacheKeys = append(cacheKeys, keysByUser[id]...)
	}
	if len(result.UserIDs) > 0 {
		if err := cleanupArchiveIdentities(ctx, tx, result.UserIDs, previewID); err != nil {
			return nil, err
		}
		// Same credential-free tombstone semantics as APIKeyRepository.DeleteWithAudit.
		// Existing migration 184 additionally enqueues durable auth cache invalidations.
		if _, err := tx.ExecContext(ctx, `UPDATE api_keys SET key='__deleted__cleanup__'||id::text||'__'||$2,deleted_at=NOW(),updated_at=NOW() WHERE user_id=ANY($1) AND deleted_at IS NULL`, pq.Array(result.UserIDs), previewID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET deleted_at=NOW(),updated_at=NOW(),status='disabled' WHERE id=ANY($1) AND deleted_at IS NULL`, pq.Array(result.UserIDs)); err != nil {
			return nil, err
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT NOW()`).Scan(&result.CompletedAt); err != nil {
		return nil, err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if err := cleanupWriteAudit(ctx, tx, actor, "admin.users.cleanup", audit, map[string]any{"preview_id": previewID, "deleted_count": result.DeletedCount, "zero_balance": result.ZeroBalance, "negative_balance": result.NegativeBalance, "skipped_count": result.SkippedCount, "user_ids": result.UserIDs}); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE user_cleanup_previews SET completed_at=NOW(),result=$2::jsonb WHERE id=$1`, previewID, string(data)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if s.invalidator != nil {
		for _, key := range cacheKeys {
			s.invalidator.InvalidateAuthCacheByKey(ctx, key)
		}
		for _, id := range result.UserIDs {
			s.invalidator.InvalidateAuthCacheByUserID(ctx, id)
		}
	}
	return result, nil
}

func cleanupLockKeys(ctx context.Context, tx *sql.Tx, ids []int64) (map[int64][]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT user_id,key FROM api_keys WHERE user_id=ANY($1) ORDER BY id FOR UPDATE`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	keys := make(map[int64][]string)
	for rows.Next() {
		var id int64
		var key string
		if err := rows.Scan(&id, &key); err != nil {
			return nil, err
		}
		// Keys are only held in memory for invalidation, never in audit/snapshots.
		keys[id] = append(keys[id], key)
	}
	return keys, rows.Err()
}

func (s *UserCleanupService) GetGuard(ctx context.Context, actorID, userID int64) (*UserCleanupGuard, error) {
	if _, err := cleanupActor(ctx, s.db, actorID); err != nil {
		return nil, err
	}
	return cleanupReadGuard(ctx, s.db, userID)
}

func cleanupReadGuard(ctx context.Context, q userCleanupQuerier, userID int64) (*UserCleanupGuard, error) {
	out := &UserCleanupGuard{UserID: userID}
	err := q.QueryRowContext(ctx, `SELECT COALESCE(g.is_system,FALSE),COALESCE(g.is_protected,FALSE),u.role<>'user' FROM users u LEFT JOIN user_cleanup_guards g ON g.user_id=u.id WHERE u.id=$1 AND u.deleted_at IS NULL`, userID).Scan(&out.IsSystem, &out.IsProtected, &out.RoleProtected)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	return out, err
}

func (s *UserCleanupService) UpdateGuard(ctx context.Context, actorID, userID int64, protected bool, system *bool, audit UserCleanupAuditContext) (*UserCleanupGuard, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	actor, err := cleanupActor(ctx, tx, actorID)
	if err != nil {
		return nil, err
	}
	var role string
	if err := tx.QueryRowContext(ctx, `SELECT role FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, userID).Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	if role != "user" {
		return nil, infraerrors.Forbidden("USER_CLEANUP_ROLE_PROTECTED", "管理与系统角色已自动受保护")
	}
	before, err := cleanupReadGuard(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	nextSystem := before.IsSystem
	if system != nil && *system != before.IsSystem {
		if actor.Role != "super_admin" {
			return nil, infraerrors.Forbidden("USER_CLEANUP_SYSTEM_GUARD", "Only a super administrator can change system account protection")
		}
		nextSystem = *system
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_cleanup_guards(user_id,is_system,is_protected,updated_by) VALUES ($1,$2,$3,$4) ON CONFLICT(user_id) DO UPDATE SET is_system=EXCLUDED.is_system,is_protected=EXCLUDED.is_protected,updated_by=EXCLUDED.updated_by,updated_at=NOW()`, userID, nextSystem, protected, actor.ID); err != nil {
		return nil, err
	}
	if err := cleanupWriteAudit(ctx, tx, actor, "admin.users.cleanup_guard.update", audit, map[string]any{"user_id": userID, "before": before, "is_system": nextSystem, "is_protected": protected}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &UserCleanupGuard{UserID: userID, IsSystem: nextSystem, IsProtected: protected}, nil
}

func cleanupWriteAudit(ctx context.Context, tx *sql.Tx, actor *UserCleanupActor, action string, audit UserCleanupAuditContext, extra map[string]any) error {
	data, err := json.Marshal(extra)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_logs(actor_user_id,actor_email,actor_role,auth_method,action,method,path,request_id,client_ip,status_code,extra) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,200,$10::jsonb)`, actor.ID, actor.Email, actor.Role, cleanupLimit(audit.AuthMethod, 32), action, cleanupLimit(audit.Method, 16), cleanupLimit(audit.Path, 512), cleanupLimit(audit.RequestID, 64), cleanupLimit(audit.ClientIP, 64), string(data))
	return err
}

func cleanupLimit(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) > limit {
		return value[:limit]
	}
	return value
}
