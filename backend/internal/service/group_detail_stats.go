package service

import (
	"context"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"time"
)

type GroupDetailStats struct {
	GroupID            int64      `json:"group_id"`
	GroupName          string     `json:"group_name"`
	TotalAPIKeys       int64      `json:"total_api_keys"`
	ActiveAPIKeys      int64      `json:"active_api_keys"`
	TotalAccounts      int64      `json:"total_accounts"`
	TotalRequests      int64      `json:"total_requests"`
	TotalTokens        int64      `json:"total_tokens"`
	TotalCost          float64    `json:"total_cost"`
	TotalActualCost    float64    `json:"total_actual_cost"`
	TotalAccountCost   float64    `json:"total_account_cost"`
	BalanceCost        float64    `json:"balance_cost"`
	SubscriptionCost   float64    `json:"subscription_cost"`
	ZeroChargeRequests int64      `json:"zero_charge_requests"`
	AverageDurationMS  float64    `json:"average_duration_ms"`
	From               *time.Time `json:"from"`
	To                 *time.Time `json:"to"`
	GeneratedAt        time.Time  `json:"generated_at"`
}
type GroupDetailStatsProvider interface {
	GetGroupDetailStats(context.Context, int64, *time.Time, *time.Time) (*GroupDetailStats, error)
}

func (s *DashboardService) GetGroupDetailStats(ctx context.Context, id int64, from, to *time.Time) (*GroupDetailStats, error) {
	if id <= 0 || (from != nil && to != nil && !from.Before(*to)) {
		return nil, infraerrors.BadRequest("GROUP_STATS_INVALID_RANGE", "统计时间范围无效")
	}
	if s == nil {
		return nil, infraerrors.ServiceUnavailable("GROUP_STATS_UNAVAILABLE", "统计服务暂不可用")
	}
	provider, ok := s.usageRepo.(GroupDetailStatsProvider)
	if !ok {
		return nil, infraerrors.ServiceUnavailable("GROUP_STATS_UNAVAILABLE", "统计服务暂不可用")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return provider.GetGroupDetailStats(ctx, id, from, to)
}
