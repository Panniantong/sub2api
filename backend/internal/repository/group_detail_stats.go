package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"time"
)

func (r *usageLogRepository) GetGroupDetailStats(ctx context.Context, id int64, from, to *time.Time) (*service.GroupDetailStats, error) {
	out := &service.GroupDetailStats{GroupID: id, From: from, To: to, GeneratedAt: time.Now().UTC()}
	// Independent aggregates avoid multiplying usage by the number of keys or
	// accounts. Historical charges belong to usage_logs.group_id even when a
	// key is later moved or deleted. Account cost uses each log's saved pricing.
	query := `SELECT g.name,
(SELECT COUNT(*) FROM api_keys k WHERE k.group_id=g.id AND k.deleted_at IS NULL),
(SELECT COUNT(*) FROM api_keys k WHERE k.group_id=g.id AND k.deleted_at IS NULL AND k.status='active' AND (k.expires_at IS NULL OR k.expires_at>NOW())),
(SELECT COUNT(DISTINCT ag.account_id) FROM account_groups ag JOIN accounts a ON a.id=ag.account_id WHERE ag.group_id=g.id AND a.deleted_at IS NULL),
u.requests,u.tokens,u.cost,u.actual,u.account_cost,u.balance,u.subscription,u.zero_charge,u.duration
FROM groups g CROSS JOIN LATERAL (
SELECT COUNT(*) AS requests,
COALESCE(SUM(input_tokens::bigint+output_tokens+cache_creation_tokens+cache_read_tokens),0) AS tokens,
COALESCE(SUM(total_cost),0) AS cost,
COALESCE(SUM(actual_cost),0) AS actual,
COALESCE(SUM(COALESCE(account_stats_cost,total_cost)*COALESCE(account_rate_multiplier,1)),0) AS account_cost,
COALESCE(SUM(actual_cost) FILTER(WHERE billing_type=0),0) AS balance,
COALESCE(SUM(actual_cost) FILTER(WHERE billing_type=1),0) AS subscription,
COUNT(*) FILTER(WHERE actual_cost=0) AS zero_charge,
COALESCE(AVG(duration_ms),0) AS duration
FROM usage_logs WHERE group_id=g.id AND ($2::timestamptz IS NULL OR created_at >= $2) AND ($3::timestamptz IS NULL OR created_at < $3)
) u WHERE g.id=$1 AND g.deleted_at IS NULL`
	err := scanSingleRow(ctx, r.sql, query, []any{id, from, to}, &out.GroupName, &out.TotalAPIKeys, &out.ActiveAPIKeys, &out.TotalAccounts, &out.TotalRequests, &out.TotalTokens, &out.TotalCost, &out.TotalActualCost, &out.TotalAccountCost, &out.BalanceCost, &out.SubscriptionCost, &out.ZeroChargeRequests, &out.AverageDurationMS)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrGroupNotFound
	}
	return out, err
}
