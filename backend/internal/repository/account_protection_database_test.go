//go:build protectionintegration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	entschema "entgo.io/ent/dialect/sql/schema"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/migrate"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func protectionTestDatabase(t *testing.T) (*dbent.Client, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("PROTECTION_TEST_DSN")
	if dsn == "" {
		if os.Getenv("REQUIRE_DATABASE_TESTS") == "1" {
			t.Fatal("PROTECTION_TEST_DSN is required in the database CI job")
		}
		t.Skip("PROTECTION_TEST_DSN must name an isolated test database")
	}
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	require.Contains(t, strings.ToLower(u.Path), "test", "refusing a non-test database")
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := fmt.Sprintf("protection_check_%d", time.Now().UnixNano())
	_, err = admin.ExecContext(context.Background(), "CREATE SCHEMA "+pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() {
		_ = client.Close()
		_, dropErr := admin.ExecContext(context.Background(), "DROP SCHEMA "+pq.QuoteIdentifier(schema)+" CASCADE")
		require.NoError(t, dropErr)
		_ = admin.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	require.NoError(t, migrate.Create(ctx, client.Schema, []*entschema.Table{migrate.UsersTable, migrate.ProxiesTable, migrate.GroupsTable, migrate.AccountsTable, migrate.AccountGroupsTable}))
	for _, filename := range []string{"036_scheduler_outbox.sql", "152_scheduler_outbox_dedup_key.sql", "153_scheduler_outbox_pending_dedup_key_index_notx.sql"} {
		body, err := migrations.FS.ReadFile(filename)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, string(body))
		require.NoError(t, err)
	}
	return client, db
}

func TestAccountProtectionRolesMigrationKeepsDataAndIsIdempotent(t *testing.T) {
	client, db := protectionTestDatabase(t)
	ctx := context.Background()
	create := func(email, role, status string, balance float64) int64 {
		u, err := client.User.Create().SetEmail(email).SetPasswordHash("test-hash").SetRole(role).SetStatus(status).SetBalance(balance).Save(ctx)
		require.NoError(t, err)
		return u.ID
	}
	_ = create("inactive@example.invalid", service.RoleAdmin, service.StatusDisabled, 15)
	first := create("first@example.invalid", service.RoleAdmin, service.StatusActive, 21.5)
	second := create("second@example.invalid", service.RoleAdmin, service.StatusActive, 12)
	user := create("user@example.invalid", service.RoleUser, service.StatusActive, -9)
	sqlText, err := migrations.FS.ReadFile("246_account_protection_roles.sql")
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = db.ExecContext(ctx, string(sqlText))
		require.NoError(t, err)
	}
	for _, tt := range []struct {
		id      int64
		role    string
		balance float64
	}{{first, service.RoleSuperAdmin, 21.5}, {second, service.RoleAdmin, 12}, {user, service.RoleUser, -9}} {
		got, err := client.User.Get(ctx, tt.id)
		require.NoError(t, err)
		require.Equal(t, tt.role, got.Role)
		require.Equal(t, tt.balance, got.Balance)
		require.Equal(t, "test-hash", got.PasswordHash)
	}
}
