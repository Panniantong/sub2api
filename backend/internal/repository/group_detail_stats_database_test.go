//go:build protectionintegration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupDetailStatsHistoricalCostsAndCurrentBindings(t *testing.T) {
	client, db := protectionTestDatabase(t)
	for i, name := range []string{"Stats group", "Other"} {
		group, err := client.Group.Create().SetName(name).SetPlatform("openai").SetStatus("active").Save(context.Background())
		require.NoError(t, err)
		_, err = db.Exec(`UPDATE groups SET id=$1 WHERE id=$2`, 900+i, group.ID)
		require.NoError(t, err)
	}
	_, err := db.Exec(`CREATE TABLE api_keys(id BIGINT PRIMARY KEY,group_id BIGINT,status TEXT,expires_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ);
INSERT INTO api_keys VALUES(1,900,'active',NULL,NULL),(2,900,'active',NOW()-INTERVAL '1 day',NULL),(3,900,'disabled',NULL,NULL),(4,900,'active',NULL,NOW()),(5,901,'active',NULL,NULL);
CREATE TABLE usage_logs(id BIGINT PRIMARY KEY,group_id BIGINT,input_tokens INT,output_tokens INT,cache_creation_tokens INT,cache_read_tokens INT,total_cost NUMERIC,actual_cost NUMERIC,account_stats_cost NUMERIC,account_rate_multiplier NUMERIC,billing_type INT,duration_ms INT,created_at TIMESTAMPTZ);
INSERT INTO usage_logs VALUES
(1,900,10,20,0,5,10,12,NULL,0.5,0,1000,'2026-09-10T00:00:00Z'),
(2,900,1,2,3,4,20,4,3,2,1,3000,'2026-09-11T00:00:00Z'),
(3,900,2,2,0,0,2,0,NULL,NULL,0,2000,'2026-09-12T00:00:00Z'),
(4,901,99,99,0,0,999,999,999,1,0,9999,'2026-09-10T00:00:00Z');`)
	require.NoError(t, err)
	repo := &usageLogRepository{sql: db}
	stats, err := repo.GetGroupDetailStats(context.Background(), 900, nil, nil)
	require.NoError(t, err)
	require.EqualValues(t, 3, stats.TotalAPIKeys)
	require.EqualValues(t, 1, stats.ActiveAPIKeys)
	require.EqualValues(t, 3, stats.TotalRequests)
	require.EqualValues(t, 49, stats.TotalTokens)
	require.Equal(t, 32.0, stats.TotalCost)
	require.Equal(t, 16.0, stats.TotalActualCost)
	require.Equal(t, 13.0, stats.TotalAccountCost)
	require.Equal(t, 12.0, stats.BalanceCost)
	require.Equal(t, 4.0, stats.SubscriptionCost)
	require.EqualValues(t, 1, stats.ZeroChargeRequests)
	require.Equal(t, 2000.0, stats.AverageDurationMS)
	// Moving/deleting a key cannot move the original usage out of its historical group.
	_, err = db.Exec(`UPDATE api_keys SET group_id=901 WHERE id=1`)
	require.NoError(t, err)
	from := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	stats, err = repo.GetGroupDetailStats(context.Background(), 900, &from, &to)
	require.NoError(t, err)
	require.EqualValues(t, 1, stats.TotalRequests)
	require.Equal(t, 20.0, stats.TotalCost)
	require.Equal(t, 6.0, stats.TotalAccountCost)
	require.EqualValues(t, 2, stats.TotalAPIKeys)
	_, err = repo.GetGroupDetailStats(context.Background(), 999, nil, nil)
	require.ErrorIs(t, err, service.ErrGroupNotFound)
	stats, err = repo.GetGroupDetailStats(context.Background(), 901, &to, nil)
	require.NoError(t, err)
	require.Zero(t, stats.TotalRequests)
	require.Zero(t, stats.TotalCost)
}
