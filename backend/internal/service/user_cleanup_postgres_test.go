package service

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// This suite never targets the deployed database. Supply an isolated database
// whose name ends in _test; every case uses its own temporary, random schema.
func cleanupTestDB(t *testing.T) (*sql.DB, *UserCleanupService, int64) {
	t.Helper()
	dsn := os.Getenv("USER_CLEANUP_TEST_DSN")
	if dsn == "" {
		if os.Getenv("REQUIRE_DATABASE_TESTS") == "1" {
			t.Fatal("USER_CLEANUP_TEST_DSN is required in the database CI job")
		}
		t.Skip("USER_CLEANUP_TEST_DSN is not set; real PostgreSQL cleanup tests require an isolated *_test database")
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		require.NoError(t, err)
		q := parsed.Query()
		q.Set("connect_timeout", "10")
		parsed.RawQuery = q.Encode()
		dsn = parsed.String()
	} else {
		dsn += " connect_timeout=10"
	}
	base, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	var database string
	require.NoError(t, base.QueryRow(`SELECT current_database()`).Scan(&database))
	require.True(t, strings.HasSuffix(database, "_test"), "refusing any database without the _test suffix")
	schema := "cleanup_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = base.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		require.NoError(t, err)
		q := parsed.Query()
		q.Set("search_path", schema)
		parsed.RawQuery = q.Encode()
		dsn = parsed.String()
	} else {
		dsn += " search_path=" + schema
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(10)
	t.Cleanup(func() {
		_ = db.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, cleanupErr := base.ExecContext(cleanupCtx, `DROP SCHEMA `+schema+` CASCADE`)
		if cleanupErr != nil {
			t.Errorf("remove isolated cleanup schema: %v", cleanupErr)
		}
		_ = base.Close()
	})
	_, err = db.Exec(cleanupTestSchema)
	require.NoError(t, err)
	for _, name := range []string{"245_user_cleanup.sql", "245_user_cleanup.sql"} {
		migration, err := migrations.FS.ReadFile(name)
		require.NoError(t, err)
		_, err = db.Exec(string(migration))
		require.NoError(t, err)
	}
	var actor int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,role) VALUES ('super@example.test','super_admin') RETURNING id`).Scan(&actor))
	return db, NewUserCleanupService(db, nil), actor
}

const cleanupTestSchema = `
CREATE TABLE users(id BIGSERIAL PRIMARY KEY,email TEXT NOT NULL DEFAULT '',username TEXT NOT NULL DEFAULT '',role TEXT NOT NULL DEFAULT 'user',status TEXT NOT NULL DEFAULT 'active',balance NUMERIC(20,8) NOT NULL DEFAULT 0,frozen_balance NUMERIC(20,8) NOT NULL DEFAULT 0,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()-INTERVAL '7 days',updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),last_login_at TIMESTAMPTZ,last_active_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ);
CREATE UNIQUE INDEX cleanup_user_email_active ON users(email) WHERE deleted_at IS NULL AND email<>'';
CREATE TABLE usage_logs(id BIGSERIAL PRIMARY KEY,user_id BIGINT REFERENCES users(id),created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()-INTERVAL '2 days');
CREATE TABLE api_keys(id BIGSERIAL PRIMARY KEY,user_id BIGINT REFERENCES users(id),key TEXT NOT NULL UNIQUE,deleted_at TIMESTAMPTZ,last_used_at TIMESTAMPTZ,updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE user_subscriptions(user_id BIGINT REFERENCES users(id),expires_at TIMESTAMPTZ,status TEXT,deleted_at TIMESTAMPTZ);
CREATE TABLE payment_orders(user_id BIGINT,expires_at TIMESTAMPTZ,status TEXT);
CREATE TABLE batch_image_jobs(user_id BIGINT,status TEXT,hold_amount NUMERIC(20,8),settled_at TIMESTAMPTZ);
CREATE TABLE audit_logs(id BIGSERIAL PRIMARY KEY,actor_user_id BIGINT,actor_email TEXT,actor_role TEXT,auth_method TEXT,action TEXT,method TEXT,path TEXT,request_id TEXT,client_ip TEXT,status_code INT,extra JSONB);
CREATE TABLE auth_identities(id BIGSERIAL PRIMARY KEY,user_id BIGINT REFERENCES users(id),provider_type TEXT,provider_key TEXT,provider_subject TEXT,metadata JSONB NOT NULL DEFAULT '{}'::jsonb,UNIQUE(provider_type,provider_key,provider_subject));
CREATE TABLE auth_identity_channels(id BIGSERIAL PRIMARY KEY,identity_id BIGINT REFERENCES auth_identities(id) ON DELETE CASCADE,channel_subject TEXT UNIQUE);
CREATE TABLE identity_adoption_decisions(id BIGSERIAL PRIMARY KEY,identity_id BIGINT REFERENCES auth_identities(id) ON DELETE SET NULL);
`

func cleanupTestUser(t *testing.T, db *sql.DB, balance string) int64 {
	t.Helper()
	var id int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,balance) VALUES ($1,$2) RETURNING id`, uuid.NewString()+"@example.test", balance).Scan(&id))
	_, err := db.Exec(`INSERT INTO usage_logs(user_id) VALUES ($1)`, id)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO api_keys(user_id,key) VALUES ($1,$2)`, id, "test-key-"+uuid.NewString())
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO auth_identities(user_id,provider_type,provider_key,provider_subject) SELECT id,'email','email',email FROM users WHERE id=$1`, id)
	require.NoError(t, err)
	return id
}

func TestUserCleanupPostgresBoundaries(t *testing.T) {
	db, svc, actor := cleanupTestDB(t)
	cases := []struct {
		name, balance, mutation string
		eligible                bool
	}{
		{"zero", "0", "", true}, {"negative", "-0.00000001", "", true}, {"positive", "0.00000001", "", false},
		{"recent_usage", "0", `UPDATE usage_logs SET created_at=NOW() WHERE user_id=$1`, false},
		{"future_usage", "0", `UPDATE usage_logs SET created_at=NOW()+INTERVAL '1 day' WHERE user_id=$1`, false},
		{"missing_history_old", "0", `DELETE FROM usage_logs WHERE user_id=$1`, false},
		{"recent_registration", "0", `UPDATE users SET created_at=NOW() WHERE id=$1`, false},
		{"admin", "0", `UPDATE users SET role='admin' WHERE id=$1`, false},
		{"super_admin", "0", `UPDATE users SET role='super_admin' WHERE id=$1`, false},
		{"system", "0", `INSERT INTO user_cleanup_guards(user_id,is_system) VALUES ($1,TRUE)`, false},
		{"protected", "0", `INSERT INTO user_cleanup_guards(user_id,is_protected) VALUES ($1,TRUE)`, false},
		{"frozen_assets", "0", `UPDATE users SET frozen_balance=1 WHERE id=$1`, false},
		{"active_subscription", "0", `INSERT INTO user_subscriptions(user_id,status,expires_at) VALUES ($1,'active',NOW()+INTERVAL '1 day')`, false},
		{"suspended_subscription", "0", `INSERT INTO user_subscriptions(user_id,status,expires_at) VALUES ($1,'suspended',NOW()+INTERVAL '1 day')`, false},
		{"expired_subscription", "0", `INSERT INTO user_subscriptions(user_id,status,expires_at) VALUES ($1,'active',NOW()-INTERVAL '1 day')`, true},
		{"pending_payment", "0", `INSERT INTO payment_orders(user_id,status,expires_at) VALUES ($1,'PENDING',NOW()+INTERVAL '1 day')`, false},
		{"recharging", "0", `INSERT INTO payment_orders(user_id,status,expires_at) VALUES ($1,'RECHARGING',NOW()-INTERVAL '1 day')`, false},
		{"historical_payment", "0", `INSERT INTO payment_orders(user_id,status,expires_at) VALUES ($1,'COMPLETED',NOW()-INTERVAL '1 day')`, true},
		{"expired_payment", "0", `INSERT INTO payment_orders(user_id,status,expires_at) VALUES ($1,'PENDING',NOW()-INTERVAL '1 day')`, true},
		{"batch_running", "0", `INSERT INTO batch_image_jobs(user_id,status) VALUES ($1,'running')`, false},
		{"batch_unsettled", "0", `INSERT INTO batch_image_jobs(user_id,status,hold_amount) VALUES ($1,'failed',1)`, false},
		{"recent_login", "0", `UPDATE users SET last_login_at=NOW() WHERE id=$1`, false},
		{"recent_activity", "0", `UPDATE users SET last_active_at=NOW() WHERE id=$1`, false},
		{"recent_api_key", "0", `UPDATE api_keys SET last_used_at=NOW() WHERE user_id=$1`, false},
	}
	want := map[int64]bool{}
	for _, tc := range cases {
		id := cleanupTestUser(t, db, tc.balance)
		if tc.mutation != "" {
			_, err := db.Exec(tc.mutation, id)
			require.NoError(t, err, tc.name)
		}
		want[id] = tc.eligible
	}
	preview, err := svc.Preview(context.Background(), actor)
	require.NoError(t, err)
	got := map[int64]bool{}
	for _, candidate := range preview.Candidates {
		got[candidate.ID] = true
	}
	for id, eligible := range want {
		require.Equal(t, eligible, got[id], "user %d", id)
	}
	require.Equal(t, 5, preview.Total)
	require.Equal(t, 4, preview.ZeroBalance)
	require.Equal(t, 1, preview.NegativeBalance)
	// NOW() remains fixed within this transaction, so the exact 24-hour boundary
	// is tested without clock drift between the fixture write and the predicate.
	tx, err := db.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`UPDATE usage_logs SET created_at=NOW()-INTERVAL '24 hours'`)
	require.NoError(t, err)
	items, err := cleanupCandidates(context.Background(), tx, nil)
	require.NoError(t, err)
	require.Empty(t, items)
}

func TestUserCleanupPostgresConcurrentRevalidationAndReplay(t *testing.T) {
	db, svc, actor := cleanupTestDB(t)
	ctx := context.Background()
	credit := cleanupTestUser(t, db, "0")
	used := cleanupTestUser(t, db, "0")
	removed := cleanupTestUser(t, db, "-1")
	preview, err := svc.Preview(ctx, actor)
	require.NoError(t, err)
	tx, err := db.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`UPDATE users SET balance=5 WHERE id=$1`, credit)
	require.NoError(t, err)
	// The insert holds PostgreSQL's FK KEY SHARE user lock until commit.
	_, err = tx.Exec(`INSERT INTO usage_logs(user_id,created_at) VALUES ($1,NOW())`, used)
	require.NoError(t, err)
	done := make(chan *UserCleanupResult, 1)
	failed := make(chan error, 1)
	go func() {
		result, err := svc.Execute(ctx, actor, preview.PreviewID, true, 3, UserCleanupAuditContext{})
		if err != nil {
			failed <- err
			return
		}
		done <- result
	}()
	// Observe an actual lock waiter, rather than treating elapsed time as evidence.
	require.Eventually(t, func() bool {
		var waiting bool
		err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT id FROM users WHERE id=ANY%')`).Scan(&waiting)
		return err == nil && waiting
	}, 10*time.Second, 50*time.Millisecond)
	require.NoError(t, tx.Commit())
	var result *UserCleanupResult
	select {
	case result = <-done:
	case err := <-failed:
		require.NoError(t, err)
	case <-time.After(20 * time.Second):
		t.Fatal("cleanup did not finish")
	}
	require.Equal(t, []int64{removed}, result.UserIDs)
	require.Equal(t, 2, result.SkippedCount)
	require.Equal(t, 1, result.NegativeBalance)
	var deleted bool
	var key string
	require.NoError(t, db.QueryRow(`SELECT deleted_at IS NOT NULL AND status='disabled' FROM users WHERE id=$1`, removed).Scan(&deleted))
	require.True(t, deleted)
	require.NoError(t, db.QueryRow(`SELECT key FROM api_keys WHERE user_id=$1`, removed).Scan(&key))
	require.True(t, strings.HasPrefix(key, "__deleted__cleanup__"))
	// Two retries after a lost response both retrieve the persisted original result.
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			replay, err := svc.Execute(ctx, actor, preview.PreviewID, true, 3, UserCleanupAuditContext{})
			if err != nil {
				failed <- err
				return
			}
			if !replay.Replayed || replay.DeletedCount != 1 {
				failed <- fmt.Errorf("invalid replay: %+v", replay)
			}
		}()
	}
	wg.Wait()
	close(failed)
	for err := range failed {
		require.NoError(t, err)
	}
	var audits, users, usage int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='admin.users.cleanup' AND extra->'user_ids'=$1::jsonb`, fmt.Sprintf("[%d]", removed)).Scan(&audits))
	require.Equal(t, 1, audits)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users))
	require.Equal(t, 4, users, "no physical user deletion")
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM usage_logs`).Scan(&usage))
	require.Equal(t, 4, usage, "history retained")
}

func TestUserCleanupPostgresAuditRollbackAndPermissions(t *testing.T) {
	db, svc, actor := cleanupTestDB(t)
	ctx := context.Background()
	user := cleanupTestUser(t, db, "0")
	cleanupTestIdentityRelations(t, db, user)
	preview, err := svc.Preview(ctx, actor)
	require.NoError(t, err)
	_, err = svc.Execute(ctx, user, preview.PreviewID, true, 1, UserCleanupAuditContext{})
	require.ErrorIs(t, err, ErrUserCleanupForbidden)
	var other int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(role) VALUES ('admin') RETURNING id`).Scan(&other))
	_, err = svc.Execute(ctx, other, preview.PreviewID, true, 1, UserCleanupAuditContext{})
	require.ErrorIs(t, err, ErrUserCleanupPreview)
	_, err = svc.Execute(ctx, actor, preview.PreviewID, true, 2, UserCleanupAuditContext{})
	require.ErrorIs(t, err, ErrUserCleanupConfirmation)
	_, err = db.Exec(`ALTER TABLE audit_logs ADD CONSTRAINT reject_cleanup CHECK(action<>'admin.users.cleanup')`)
	require.NoError(t, err)
	_, err = svc.Execute(ctx, actor, preview.PreviewID, true, 1, UserCleanupAuditContext{})
	require.Error(t, err)
	var intact bool
	require.NoError(t, db.QueryRow(`SELECT u.deleted_at IS NULL AND k.deleted_at IS NULL AND k.key NOT LIKE '__deleted__%' FROM users u JOIN api_keys k ON k.user_id=u.id WHERE u.id=$1`, user).Scan(&intact))
	require.True(t, intact, "audit failure must roll back both user and key mutation")
	var identityIntact bool
	require.NoError(t, db.QueryRow(`SELECT EXISTS(SELECT 1 FROM auth_identities WHERE user_id=$1) AND NOT EXISTS(SELECT 1 FROM user_cleanup_identity_archives WHERE user_id=$1)`, user).Scan(&identityIntact))
	require.True(t, identityIntact, "audit failure must roll back identity archiving and unbinding")
	require.NoError(t, db.QueryRow(`SELECT EXISTS(SELECT 1 FROM auth_identity_channels c JOIN auth_identities a ON a.id=c.identity_id WHERE a.user_id=$1) AND EXISTS(SELECT 1 FROM identity_adoption_decisions d JOIN auth_identities a ON a.id=d.identity_id WHERE a.user_id=$1)`, user).Scan(&identityIntact))
	require.True(t, identityIntact, "audit failure must also roll back channel deletion and decision detachment")
	_, err = db.Exec(`ALTER TABLE audit_logs DROP CONSTRAINT reject_cleanup`)
	require.NoError(t, err)
	system := true
	_, err = svc.UpdateGuard(ctx, other, user, false, &system, UserCleanupAuditContext{})
	require.Error(t, err, "admin cannot mark system accounts")
	_, err = svc.UpdateGuard(ctx, actor, user, false, &system, UserCleanupAuditContext{})
	require.NoError(t, err)
	result, err := svc.Execute(ctx, actor, preview.PreviewID, true, 1, UserCleanupAuditContext{})
	require.NoError(t, err)
	require.Zero(t, result.DeletedCount)
	require.Equal(t, 1, result.SkippedCount)
	_, err = svc.UpdateGuard(ctx, actor, other, true, nil, UserCleanupAuditContext{})
	require.Error(t, err, "administrators are inherently protected")
}

func TestUserCleanupRequiresExplicitConfirmation(t *testing.T) {
	svc := NewUserCleanupService(nil, nil)
	for _, count := range []int{-1, 0, 501} {
		_, err := svc.Execute(context.Background(), 1, uuid.NewString(), true, count, UserCleanupAuditContext{})
		require.ErrorIs(t, err, ErrUserCleanupConfirmation)
	}
	_, err := svc.Execute(context.Background(), 1, uuid.NewString(), false, 1, UserCleanupAuditContext{})
	require.ErrorIs(t, err, ErrUserCleanupConfirmation)
	_, err = svc.Execute(context.Background(), 1, "invalid", true, 1, UserCleanupAuditContext{})
	require.ErrorIs(t, err, ErrUserCleanupPreview)
}

func TestUserCleanupPostgresConcurrentActivityLocks(t *testing.T) {
	for _, tc := range []struct{ name, mutation, waitingQuery string }{
		{"usage_fk", `INSERT INTO usage_logs(user_id,created_at) VALUES ($1,NOW())`, "SELECT id FROM users WHERE id=ANY%"},
		{"api_key_ingress", `UPDATE api_keys SET last_used_at=NOW() WHERE user_id=$1`, "SELECT user_id,key FROM api_keys WHERE user_id=ANY%"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, svc, actor := cleanupTestDB(t)
			user := cleanupTestUser(t, db, "0")
			ctx := context.Background()
			preview, err := svc.Preview(ctx, actor)
			require.NoError(t, err)
			tx, err := db.Begin()
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()
			_, err = tx.Exec(tc.mutation, user)
			require.NoError(t, err)
			type outcome struct {
				result *UserCleanupResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := svc.Execute(ctx, actor, preview.PreviewID, true, 1, UserCleanupAuditContext{})
				done <- outcome{result, err}
			}()
			require.Eventually(t, func() bool {
				var waiting bool
				err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE $1)`, tc.waitingQuery).Scan(&waiting)
				return err == nil && waiting
			}, 10*time.Second, 50*time.Millisecond)
			require.NoError(t, tx.Commit())
			select {
			case result := <-done:
				require.NoError(t, result.err)
				require.Zero(t, result.result.DeletedCount)
				require.Equal(t, 1, result.result.SkippedCount)
			case <-time.After(20 * time.Second):
				t.Fatal("cleanup did not finish")
			}
		})
	}
}

func TestUserCleanupPostgresConcurrentDuplicateExecutions(t *testing.T) {
	db, svc, actor := cleanupTestDB(t)
	ctx := context.Background()
	cleanupTestUser(t, db, "0")
	preview, err := svc.Preview(ctx, actor)
	require.NoError(t, err)
	type outcome struct {
		result *UserCleanupResult
		err    error
	}
	done := make(chan outcome, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			result, err := svc.Execute(ctx, actor, preview.PreviewID, true, 1, UserCleanupAuditContext{})
			done <- outcome{result, err}
		}()
	}
	close(start)
	replays := 0
	for range 2 {
		select {
		case result := <-done:
			require.NoError(t, result.err)
			require.Equal(t, 1, result.result.DeletedCount)
			if result.result.Replayed {
				replays++
			}
		case <-time.After(20 * time.Second):
			t.Fatal("concurrent execution did not finish")
		}
	}
	require.Equal(t, 1, replays)
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='admin.users.cleanup'`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestUserCleanupPostgresPreviewExpiryAndBoundedScope(t *testing.T) {
	db, svc, actor := cleanupTestDB(t)
	ctx := context.Background()
	cleanupTestUser(t, db, "0")
	preview, err := svc.Preview(ctx, actor)
	require.NoError(t, err)
	addedLater := cleanupTestUser(t, db, "0")
	_, err = db.Exec(`UPDATE user_cleanup_previews SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, preview.PreviewID)
	require.NoError(t, err)
	_, err = svc.Execute(ctx, actor, preview.PreviewID, true, 1, UserCleanupAuditContext{})
	require.ErrorIs(t, err, ErrUserCleanupPreview)
	_, err = db.Exec(`UPDATE user_cleanup_previews SET expires_at=NOW()+INTERVAL '1 minute' WHERE id=$1`, preview.PreviewID)
	require.NoError(t, err)
	result, err := svc.Execute(ctx, actor, preview.PreviewID, true, 1, UserCleanupAuditContext{})
	require.NoError(t, err)
	require.Equal(t, 1, result.DeletedCount)
	var intact bool
	require.NoError(t, db.QueryRow(`SELECT deleted_at IS NULL FROM users WHERE id=$1`, addedLater).Scan(&intact))
	require.True(t, intact, "users outside the confirmed snapshot must remain intact")
}

func cleanupTestIdentityRelations(t *testing.T, db *sql.DB, userID int64) {
	t.Helper()
	_, err := db.Exec(`WITH added AS (INSERT INTO auth_identity_channels(identity_id,channel_subject) SELECT id,'channel-'||id::text FROM auth_identities WHERE user_id=$1 RETURNING identity_id) INSERT INTO identity_adoption_decisions(identity_id) SELECT identity_id FROM added`, userID)
	require.NoError(t, err)
}

func TestUserCleanupPostgresArchivesBindingsAndReleasesRegistrationIdentity(t *testing.T) {
	db, svc, actor := cleanupTestDB(t)
	ctx := context.Background()
	oldUser := cleanupTestUser(t, db, "0")
	cleanupTestIdentityRelations(t, db, oldUser)
	_, err := db.Exec(`UPDATE auth_identities SET metadata='{"profile_note":"private-identity-metadata"}'::jsonb WHERE user_id=$1`, oldUser)
	require.NoError(t, err)
	preview, err := svc.Preview(ctx, actor)
	require.NoError(t, err)
	result, err := svc.Execute(ctx, actor, preview.PreviewID, true, 1, UserCleanupAuditContext{})
	require.NoError(t, err)
	require.Equal(t, 1, result.DeletedCount)
	var archived bool
	require.NoError(t, db.QueryRow(`SELECT jsonb_array_length(identities)=1 AND jsonb_array_length(channels)=1 AND jsonb_array_length(adoption_decisions)=1 AND identities->0->'metadata'->>'profile_note'='private-identity-metadata' FROM user_cleanup_identity_archives WHERE user_id=$1`, oldUser).Scan(&archived))
	require.True(t, archived)
	var detached bool
	require.NoError(t, db.QueryRow(`SELECT COUNT(*)=1 AND COUNT(identity_id)=0 FROM identity_adoption_decisions`).Scan(&detached))
	require.True(t, detached, "adoption history retained with detached live binding")
	var newUser int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email) SELECT email FROM users WHERE id=$1 RETURNING id`, oldUser).Scan(&newUser))
	// The exact canonical identity uniqueness tuple used by repository registration
	// can be claimed by the newly registered same-email user after cleanup.
	_, err = db.Exec(`INSERT INTO auth_identities(user_id,provider_type,provider_key,provider_subject) SELECT id,'email','email',email FROM users WHERE id=$1`, newUser)
	require.NoError(t, err)
	var oldPreserved bool
	require.NoError(t, db.QueryRow(`SELECT deleted_at IS NOT NULL AND EXISTS(SELECT 1 FROM usage_logs WHERE user_id=$1) FROM users WHERE id=$1`, oldUser).Scan(&oldPreserved))
	require.True(t, oldPreserved)
	var leaked bool
	require.NoError(t, db.QueryRow(`SELECT EXISTS(SELECT 1 FROM audit_logs WHERE extra::text LIKE '%private-identity-metadata%') OR EXISTS(SELECT 1 FROM user_cleanup_previews WHERE candidate_snapshot::text LIKE '%private-identity-metadata%' OR result::text LIKE '%private-identity-metadata%')`).Scan(&leaked))
	require.False(t, leaked, "private identity archive must not leak into returned preview/result or operation logs")
}
