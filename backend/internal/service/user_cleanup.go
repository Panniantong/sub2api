package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

const UserCleanupBatchLimit = 500

var (
	ErrUserCleanupForbidden    = infraerrors.Forbidden("USER_CLEANUP_FORBIDDEN", "Only an active administrator can manage user cleanup")
	ErrUserCleanupPreview      = infraerrors.Conflict("USER_CLEANUP_PREVIEW_EXPIRED", "清理预览不存在或已过期，请重新预览")
	ErrUserCleanupConfirmation = infraerrors.BadRequest("USER_CLEANUP_CONFIRMATION_REQUIRED", "请确认本次预览中的用户数量后再清理")
	ErrUserCleanupBusy         = infraerrors.Conflict("USER_CLEANUP_BUSY", "清理预览或用户数据正在处理，请保留本次预览稍后重试")
)

type UserCleanupActor struct {
	ID    int64
	Email string
	Role  string
}

type UserCleanupCandidate struct {
	ID          int64     `json:"id"`
	Email       string    `json:"email"`
	Username    string    `json:"username"`
	Balance     string    `json:"balance"`
	LastUsedAt  time.Time `json:"last_used_at"`
	CreatedAt   time.Time `json:"created_at"`
	ZeroBalance bool      `json:"zero_balance"`
}

type UserCleanupSummary struct {
	Total           int       `json:"total"`
	ZeroBalance     int       `json:"zero_balance"`
	NegativeBalance int       `json:"negative_balance"`
	ServerTime      time.Time `json:"server_time"`
	Cutoff          time.Time `json:"cutoff"`
	BatchLimit      int       `json:"batch_limit"`
}

type UserCleanupPreview struct {
	UserCleanupSummary
	PreviewID  string                 `json:"preview_id"`
	ExpiresAt  time.Time              `json:"expires_at"`
	Candidates []UserCleanupCandidate `json:"candidates"`
}

type UserCleanupResult struct {
	PreviewID       string    `json:"preview_id"`
	DeletedCount    int       `json:"deleted_count"`
	ZeroBalance     int       `json:"zero_balance"`
	NegativeBalance int       `json:"negative_balance"`
	SkippedCount    int       `json:"skipped_count"`
	UserIDs         []int64   `json:"user_ids"`
	CompletedAt     time.Time `json:"completed_at"`
	Replayed        bool      `json:"replayed"`
}

type UserCleanupGuard struct {
	UserID        int64 `json:"user_id"`
	IsSystem      bool  `json:"is_system"`
	IsProtected   bool  `json:"is_protected"`
	RoleProtected bool  `json:"role_protected"`
}

type UserCleanupService struct {
	db          *sql.DB
	invalidator APIKeyAuthCacheInvalidator
}

func NewUserCleanupService(db *sql.DB, invalidator APIKeyAuthCacheInvalidator) *UserCleanupService {
	return &UserCleanupService{db: db, invalidator: invalidator}
}

type userCleanupQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func cleanupActor(ctx context.Context, q userCleanupQuerier, id int64) (*UserCleanupActor, error) {
	actor := &UserCleanupActor{ID: id}
	if id <= 0 {
		return nil, ErrUserCleanupForbidden
	}
	err := q.QueryRowContext(ctx, `SELECT email, role FROM users WHERE id=$1 AND deleted_at IS NULL AND status='active' AND role IN ('admin','super_admin') FOR SHARE`, id).Scan(&actor.Email, &actor.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserCleanupForbidden
	}
	if err != nil {
		return nil, err
	}
	return actor, nil
}

func (s *UserCleanupService) Summary(ctx context.Context, actorID int64) (*UserCleanupSummary, error) {
	if _, err := cleanupActor(ctx, s.db, actorID); err != nil {
		return nil, err
	}
	return cleanupSummary(ctx, s.db)
}

func cleanupSummary(ctx context.Context, q userCleanupQuerier) (*UserCleanupSummary, error) {
	out := &UserCleanupSummary{BatchLimit: UserCleanupBatchLimit}
	err := q.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(*) FILTER (WHERE u.balance=0), COUNT(*) FILTER (WHERE u.balance<0), NOW(), NOW()-INTERVAL '24 hours' FROM users u `+userCleanupPredicate).Scan(&out.Total, &out.ZeroBalance, &out.NegativeBalance, &out.ServerTime, &out.Cutoff)
	return out, err
}

func (s *UserCleanupService) Preview(ctx context.Context, actorID int64) (*UserCleanupPreview, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := cleanupActor(ctx, tx, actorID); err != nil {
		return nil, err
	}
	summary, err := cleanupSummary(ctx, tx)
	if err != nil {
		return nil, err
	}
	candidates, err := cleanupCandidates(ctx, tx, nil)
	if err != nil {
		return nil, err
	}
	out := &UserCleanupPreview{UserCleanupSummary: *summary, PreviewID: uuid.NewString(), Candidates: candidates}
	ids := make([]int64, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.ID)
	}
	snapshot, err := json.Marshal(candidates)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO user_cleanup_previews (id,actor_user_id,candidate_ids,candidate_snapshot) VALUES ($1,$2,$3,$4::jsonb) RETURNING expires_at`, out.PreviewID, actorID, pq.Array(ids), string(snapshot)).Scan(&out.ExpiresAt)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func cleanupCandidates(ctx context.Context, q userCleanupQuerier, ids []int64) ([]UserCleanupCandidate, error) {
	query := `SELECT u.id, u.email, u.username, u.balance::text, (SELECT MAX(created_at) FROM usage_logs WHERE user_id=u.id), u.created_at, u.balance=0 FROM users u ` + userCleanupPredicate
	args := []any{}
	if ids != nil {
		query += ` AND u.id=ANY($1)`
		args = append(args, pq.Array(ids))
	}
	query += fmt.Sprintf(` ORDER BY u.id LIMIT %d`, UserCleanupBatchLimit)
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]UserCleanupCandidate, 0)
	for rows.Next() {
		var candidate UserCleanupCandidate
		if err := rows.Scan(&candidate.ID, &candidate.Email, &candidate.Username, &candidate.Balance, &candidate.LastUsedAt, &candidate.CreatedAt, &candidate.ZeroBalance); err != nil {
			return nil, err
		}
		out = append(out, candidate)
	}
	return out, rows.Err()
}

// Usage is the same persisted usage_logs.MAX(created_at) displayed by user management.
// Missing history is never evidence of inactivity, including users whose logs were purged.
// Recent registration/login/activity and unsettled assets add conservative protections.
const userCleanupPredicate = `
 WHERE u.deleted_at IS NULL AND u.role='user'
 AND u.balance<=0 AND u.frozen_balance=0
 AND u.created_at < NOW()-INTERVAL '24 hours'
 AND (u.last_login_at IS NULL OR u.last_login_at < NOW()-INTERVAL '24 hours')
 AND (u.last_active_at IS NULL OR u.last_active_at < NOW()-INTERVAL '24 hours')
 AND (SELECT MAX(created_at) FROM usage_logs WHERE user_id=u.id) < NOW()-INTERVAL '24 hours'
 AND NOT EXISTS (SELECT 1 FROM api_keys k WHERE k.user_id=u.id AND k.last_used_at>=NOW()-INTERVAL '24 hours')
 AND NOT EXISTS (SELECT 1 FROM user_cleanup_guards g WHERE g.user_id=u.id AND (g.is_system OR g.is_protected))
 AND NOT EXISTS (SELECT 1 FROM user_subscriptions s WHERE s.user_id=u.id AND s.expires_at>NOW() AND s.status IN ('active','suspended'))
 AND NOT EXISTS (SELECT 1 FROM payment_orders p WHERE p.user_id=u.id AND ((p.status='PENDING' AND p.expires_at>NOW()) OR p.status IN ('PAID','RECHARGING','REFUNDING','REFUND_PENDING','REFUND_REQUESTED','REFUND_FAILED')))
 AND NOT EXISTS (SELECT 1 FROM batch_image_jobs b WHERE b.user_id=u.id AND (b.status NOT IN ('completed','failed','cancelled','output_deleted') OR (b.hold_amount>0 AND b.settled_at IS NULL)))
`
