package repository

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/Wei-Shaw/sub2api/internal/service"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// A single session with a pg_temp-only search path keeps this opt-in test
// isolated even when its connection points at an existing development database.
func TestHarvestPostgresPersistenceAndReset(t *testing.T) {
	dsn := os.Getenv("HARVEST_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("HARVEST_TEST_POSTGRES_DSN not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	_, err = db.ExecContext(ctx, `SET search_path TO pg_temp; CREATE TEMP TABLE accounts (id BIGINT PRIMARY KEY); INSERT INTO accounts VALUES (1)`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../migrations/239_codex_harvest_node_learning.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, strings.ReplaceAll(string(migration), "CREATE TABLE IF NOT EXISTS", "CREATE TEMP TABLE"))
	require.NoError(t, err)
	scope := service.CodexHarvestNodeScope{PoolID: "pool", AccountID: 1, Identity: "identity", Model: "model", Blocks: 10}
	repo := NewCodexHarvestNodeRepository(db)
	generation, records, err := repo.Snapshot(ctx, scope)
	require.NoError(t, err)
	require.Empty(t, records)
	feedback := service.CodexHarvestNodeFeedback{Scope: scope, Node: mihomo.HarvestNode{ID: "node", Name: "Fixture", Provider: "airport"}, Generation: generation, Result: "success", LatencyMS: 120, CooldownSeconds: 180}
	stored, err := repo.Record(ctx, feedback)
	require.NoError(t, err)
	require.True(t, stored)
	feedback.Result = "invalid_state"
	stored, err = repo.Record(ctx, feedback)
	require.NoError(t, err)
	require.True(t, stored)
	feedback.Result = "rate_limited"
	_, err = repo.Record(ctx, feedback)
	require.NoError(t, err)

	restarted := NewCodexHarvestNodeRepository(db)
	_, records, err = restarted.Snapshot(ctx, scope)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.EqualValues(t, 1, records[0].Successes)
	require.EqualValues(t, 1, records[0].Misses)
	require.EqualValues(t, 1, records[0].AccountErrors)
	require.Equal(t, 1, records[0].ConsecutiveFailures)
	require.NotNil(t, records[0].LastSuccess)
	require.NotNil(t, records[0].CooldownUntil)
	page, err := restarted.List(ctx, 0, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Total)
	require.NoError(t, restarted.Reset(ctx, records[0].ID))
	stored, err = repo.Record(ctx, feedback)
	require.NoError(t, err)
	require.False(t, stored)
	generation, records, err = restarted.Snapshot(ctx, scope)
	require.NoError(t, err)
	require.Empty(t, records)
	feedback.Generation = generation
	feedback.Result = "success"
	stored, err = restarted.Record(ctx, feedback)
	require.NoError(t, err)
	require.True(t, stored)
	require.NoError(t, restarted.Reset(ctx, 0))
	page, err = restarted.List(ctx, 0, 20)
	require.NoError(t, err)
	require.Zero(t, page.Total)
}
