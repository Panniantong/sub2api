package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func intelligentTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("INTELLIGENT_TESTS_TEST_DSN")
	if dsn == "" {
		if os.Getenv("REQUIRE_DATABASE_TESTS") == "1" {
			t.Fatal("INTELLIGENT_TESTS_TEST_DSN is required in the database CI job")
		}
		t.Skip("INTELLIGENT_TESTS_TEST_DSN must point to an isolated PostgreSQL test database")
	}
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	require.True(t, parsed.Scheme == "postgres" || parsed.Scheme == "postgresql", "test DSN must be a PostgreSQL URL")
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	require.NoError(t, admin.Ping())
	schema := "it_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	q := parsed.Query()
	q.Set("search_path", schema)
	q.Set("connect_timeout", "10")
	q.Set("statement_timeout", "30000")
	parsed.RawQuery = q.Encode()
	db, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	t.Cleanup(func() {
		_ = db.Close()
		_, dropErr := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		if dropErr != nil {
			t.Error(dropErr)
		}
		_ = admin.Close()
	})
	_, err = db.Exec(`CREATE TABLE users(id BIGINT PRIMARY KEY,email TEXT NOT NULL DEFAULT '',role TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'active',deleted_at TIMESTAMPTZ,restrict_public_groups BOOLEAN NOT NULL DEFAULT false);
 CREATE TABLE accounts(id BIGINT PRIMARY KEY,name TEXT NOT NULL DEFAULT '',notes TEXT,platform TEXT NOT NULL DEFAULT 'openai',type TEXT NOT NULL DEFAULT 'apikey',status TEXT NOT NULL DEFAULT 'active',extra JSONB NOT NULL DEFAULT '{}',deleted_at TIMESTAMPTZ);
 CREATE TABLE groups(id BIGINT PRIMARY KEY,is_exclusive BOOLEAN NOT NULL DEFAULT false,status TEXT NOT NULL DEFAULT 'active',subscription_type TEXT NOT NULL DEFAULT 'standard',deleted_at TIMESTAMPTZ);
 CREATE TABLE account_groups(account_id BIGINT,group_id BIGINT);
 CREATE TABLE user_allowed_groups(user_id BIGINT,group_id BIGINT);
 CREATE TABLE user_subscriptions(user_id BIGINT,group_id BIGINT,status TEXT,expires_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ);
 CREATE TABLE audit_logs(id BIGSERIAL PRIMARY KEY,actor_user_id BIGINT,actor_email TEXT,actor_role TEXT,action TEXT,method TEXT,path TEXT,status_code INTEGER,extra JSONB);
 INSERT INTO users(id,email,role) VALUES(1,'admin@test','super_admin'),(2,'user@test','user'),(3,'other@test','user');
 INSERT INTO accounts(id,name,extra) VALUES(10,'Pro #10','{"anti_degradation":true}'),(11,'Pro #11','{"anti_degradation":false}'),(12,'Legacy','{"anti_degrade":{"enabled":true}}');
 INSERT INTO groups(id,is_exclusive) VALUES(100,true),(101,true);
 INSERT INTO account_groups VALUES(10,100),(11,101),(12,100);
 INSERT INTO user_allowed_groups VALUES(2,100),(3,101);`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../migrations/247_intelligent_tests.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err, "migration must be safely repeatable")
	migration, err = os.ReadFile("../../migrations/248_intelligent_assessment_v2.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err, "assessment migration must be safely repeatable")
	return db
}
func TestIntelligentRepositoryIntegration(t *testing.T) {
	db := intelligentTestDB(t)
	repo := &intelligentTestRepository{db: db}
	ctx := context.Background()
	ok, err := repo.IsAdmin(ctx, 1)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = repo.IsAdmin(ctx, 2)
	require.NoError(t, err)
	require.False(t, ok)
	svc := service.NewIntelligentTestService(repo, nil)
	_, err = svc.Enqueue(ctx, 2, service.IntelligentTestEnqueue{})
	require.ErrorIs(t, err, service.ErrIntelligentTestForbidden)
	request := service.IntelligentTestEnqueue{AccountIDs: []int64{10, 11, 12}, TestTypes: []string{"pelican", "candy"}, IdempotencyKey: uuid.NewString()}
	first, err := repo.Enqueue(ctx, 1, request)
	require.NoError(t, err)
	require.Len(t, first.Records, 6)
	replay, err := repo.Enqueue(ctx, 1, request)
	require.NoError(t, err)
	require.True(t, replay.Reused)
	require.Equal(t, first.Records[0].ID, replay.Records[0].ID)
	altered := request
	altered.AccountIDs = []int64{10}
	_, err = repo.Enqueue(ctx, 1, altered)
	require.ErrorIs(t, err, service.ErrIntelligentTestConflict)
	// A different idempotency key still cannot schedule duplicate active work.
	request.IdempotencyKey = uuid.NewString()
	dedup, err := repo.Enqueue(ctx, 1, request)
	require.NoError(t, err)
	require.True(t, dedup.Reused)
	require.Equal(t, first.Records[0].ID, dedup.Records[0].ID)
	var total int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM account_tests`).Scan(&total))
	require.Equal(t, 6, total)
	var wg sync.WaitGroup
	claims := make(chan *service.IntelligentTestRecord, 8)
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			record, err := repo.Claim(ctx)
			if err != nil {
				failures <- err
			}
			if record != nil {
				claims <- record
			}
		}()
	}
	wg.Wait()
	close(claims)
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	byAccount := map[int64]*service.IntelligentTestRecord{}
	for record := range claims {
		require.Nil(t, byAccount[record.AccountID], "parallel tests must not collide on one account")
		byAccount[record.AccountID] = record
	}
	require.Len(t, byAccount, 3)
	require.True(t, byAccount[10].AntiDegradation)
	require.False(t, byAccount[11].AntiDegradation)
	require.True(t, byAccount[12].AntiDegradation)
	for _, record := range byAccount {
		record.Status = "success"
		record.Result = "ANSWER: 12"
		record.RawResponse = "private raw response"
		record.ErrorMessage = "private upstream details"
		record.ResultImage = `<svg viewBox="0 0 10 10"><circle cx="5" cy="5" r="4"/></svg>`
		require.NoError(t, repo.Finish(ctx, record))
	}
	// Results are hidden by default for list, direct ID, and account history.
	owned := byAccount[10]
	_, err = repo.PublicGet(ctx, 2, owned.ID)
	require.ErrorIs(t, err, service.ErrIntelligentTestNotFound)
	f := service.IntelligentTestFilter{Page: 1, PageSize: 24}
	capabilities, count, err := repo.Capabilities(ctx, 2, f)
	require.NoError(t, err)
	require.Zero(t, count)
	require.Empty(t, capabilities)
	settings, err := repo.Settings(ctx)
	require.NoError(t, err)
	for _, setting := range settings {
		setting.UserVisible = true
		require.NoError(t, repo.UpdateSetting(ctx, 1, &setting))
	}
	public, err := repo.PublicGet(ctx, 2, owned.ID)
	require.NoError(t, err)
	require.Equal(t, "ANSWER: 12", public.Result)
	_, err = repo.PublicGet(ctx, 3, owned.ID)
	require.ErrorIs(t, err, service.ErrIntelligentTestNotFound, "a visible result still requires group authorization")
	capabilities, count, err = repo.Capabilities(ctx, 2, f)
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
	require.Len(t, capabilities, 2)
	history, err := repo.PublicRecords(ctx, 3, service.IntelligentTestFilter{AccountID: 10, Page: 1, PageSize: 24})
	require.NoError(t, err)
	require.Empty(t, history.Items)
	// Independent switches: disabling new work does not unpublish history.
	for _, setting := range settings {
		setting.UserVisible = true
		setting.Enabled = false
		require.NoError(t, repo.UpdateSetting(ctx, 1, &setting))
	}
	_, err = repo.PublicGet(ctx, 2, owned.ID)
	require.NoError(t, err)
	request.IdempotencyKey = uuid.NewString()
	_, err = repo.Enqueue(ctx, 1, request)
	require.Error(t, err)
	for _, setting := range settings {
		setting.UserVisible = false
		require.NoError(t, repo.UpdateSetting(ctx, 1, &setting))
	}
	_, err = repo.PublicGet(ctx, 2, owned.ID)
	require.ErrorIs(t, err, service.ErrIntelligentTestNotFound)
	for _, setting := range settings {
		setting.Enabled = true
		require.NoError(t, repo.UpdateSetting(ctx, 1, &setting))
	}
	// A new process sees the same queued jobs. Expired running work becomes failed,
	// never silently replayed (the upstream may already have charged it).
	restarted := &intelligentTestRepository{db: db}
	running, err := restarted.Claim(ctx)
	require.NoError(t, err)
	require.NotNil(t, running)
	_, err = db.Exec(`UPDATE account_tests SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, running.ID)
	require.NoError(t, err)
	_, err = restarted.Claim(ctx)
	require.NoError(t, err)
	expired, err := repo.Get(ctx, running.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", expired.Status)
	require.Contains(t, expired.ErrorMessage, "重复计费")
	running.Status = "success"
	require.Error(t, repo.Finish(ctx, running), "expired worker cannot overwrite recovery result")
	// Filters and overview are executed against PostgreSQL, not inferred from SQL text.
	accounts, err := repo.Accounts(ctx, service.IntelligentTestFilter{Page: 1, PageSize: 24, Search: "10"})
	require.NoError(t, err)
	require.EqualValues(t, 1, accounts.Total)
	_, err = repo.Accounts(ctx, service.IntelligentTestFilter{Page: 1, PageSize: 24, Status: "success", OnlyAbnormal: true, TestType: "pelican"})
	require.NoError(t, err)
	_, err = repo.Accounts(ctx, service.IntelligentTestFilter{Page: 1, PageSize: 24, TestType: "candy"})
	require.NoError(t, err, "test type alone must not leave unused SQL parameters")
	records, err := repo.Records(ctx, f)
	require.NoError(t, err)
	require.EqualValues(t, 6, records.Total)
	for _, record := range records.Items {
		require.Empty(t, record.RawResponse)
		require.Empty(t, record.Input)
	}
	var audits int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM audit_logs`).Scan(&audits))
	require.GreaterOrEqual(t, audits, 2)
	var accountsBefore int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM accounts`).Scan(&accountsBefore))
	require.Equal(t, 3, accountsBefore)
	t.Log(fmt.Sprintf("verified %d durable records, %d audit entries; isolated schema only", records.Total, audits))
}
