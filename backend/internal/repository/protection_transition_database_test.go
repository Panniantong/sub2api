//go:build protectionintegration

package repository

import (
	"context"
	"maps"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestProtectionTransitionDatabaseSingleCommitAndRollback(t *testing.T) {
	client, db := protectionTestDatabase(t)
	repo := newAccountRepositoryWithSQL(client, db, nil)
	a := &service.Account{Name: "transition", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Concurrency: 32, Status: service.StatusActive}
	require.NoError(t, repo.Create(context.Background(), a))
	_, err := db.Exec(`CREATE TABLE policy_observations(enabled BOOLEAN, mode TEXT);
CREATE FUNCTION observe_policy() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN INSERT INTO policy_observations VALUES ((NEW.extra->>'anti_degradation')::boolean,NEW.extra#>>'{anti_degrade,mode}'); RETURN NEW; END $$;
CREATE TRIGGER observe_policy AFTER UPDATE OF extra ON accounts FOR EACH ROW EXECUTE FUNCTION observe_policy();`)
	require.NoError(t, err)
	svc := service.NewAntiDegradeService(protectionIntegrationStore{repo})
	changed, err := svc.ApplyAntiDegradeMode(context.Background(), a.ID, service.AntiDegradeMode1)
	require.NoError(t, err)
	require.True(t, changed.IsMode1ProtectionEnabled())
	var count, disabled int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*),COUNT(*) FILTER(WHERE NOT enabled) FROM policy_observations`).Scan(&count, &disabled))
	require.Equal(t, 1, count)
	require.Zero(t, disabled)
	_, err = db.Exec(`CREATE FUNCTION reject_mode2() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.extra#>>'{anti_degrade,mode}'='mode2' THEN RAISE EXCEPTION 'test-rejected'; END IF; RETURN NEW; END $$;
CREATE TRIGGER reject_mode2 BEFORE UPDATE OF extra ON accounts FOR EACH ROW EXECUTE FUNCTION reject_mode2();`)
	require.NoError(t, err)
	_, err = svc.ApplyAntiDegradeMode(context.Background(), a.ID, service.AntiDegradeMode2)
	require.ErrorContains(t, err, "test-rejected")
	current, err := repo.GetByID(context.Background(), a.ID)
	require.NoError(t, err)
	require.True(t, current.IsMode1ProtectionEnabled())
	require.Equal(t, changed.Extra, current.Extra)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM policy_observations`).Scan(&count))
	require.Equal(t, 1, count, "failed transaction must not publish an intermediate state")
}

type staleProtectionStore struct {
	protectionIntegrationStore
	stale *service.Account
}

func TestProtectedProxyConflictIsCheckedUnderDatabaseLock(t *testing.T) {
	client, db := protectionTestDatabase(t)
	repo := newAccountRepositoryWithSQL(client, db, nil)
	ctx := context.Background()
	a := &service.Account{Name: "proxy-conflict", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Concurrency: 4, Status: service.StatusActive}
	require.NoError(t, repo.Create(ctx, a))
	stale, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	stale.Extra = map[string]any{"proxy_mode": "random"}
	stale.ProxyID = nil
	require.ErrorIs(t, repo.Update(ctx, stale), service.ErrProtectedProxyModeChange)
	actual, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, "legacy", actual.ProtectionMode())
	require.False(t, actual.IsRandomProxy())
	clear := int64(0)
	_, err = repo.BulkUpdate(ctx, []int64{a.ID}, service.AccountBulkUpdate{ProxyID: &clear, Extra: map[string]any{"proxy_mode": "random"}})
	require.ErrorIs(t, err, service.ErrProtectedProxyModeChange)
	actual, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, "legacy", actual.ProtectionMode())
}

func (s staleProtectionStore) GetAccount(context.Context, int64) (*service.Account, error) {
	return s.stale, nil
}

func TestProtectionTransitionDatabaseRejectsConcurrentRevision(t *testing.T) {
	client, db := protectionTestDatabase(t)
	repo := newAccountRepositoryWithSQL(client, db, nil)
	a := &service.Account{Name: "revision", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Concurrency: 16, Status: service.StatusActive}
	require.NoError(t, repo.Create(context.Background(), a))
	stale, err := repo.GetByID(context.Background(), a.ID)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE accounts SET notes='concurrent edit',updated_at=updated_at+INTERVAL '1 second' WHERE id=$1`, a.ID)
	require.NoError(t, err)
	svc := service.NewAntiDegradeService(staleProtectionStore{protectionIntegrationStore{repo}, stale})
	_, err = svc.ApplyAntiDegradeMode(context.Background(), a.ID, service.AntiDegradeMode1)
	require.ErrorIs(t, err, service.ErrProtectionConflict)
	current, err := repo.GetByID(context.Background(), a.ID)
	require.NoError(t, err)
	require.Equal(t, "legacy", current.ProtectionMode())
	require.Equal(t, "concurrent edit", *current.Notes)
}

func TestAccountProtectionAllStrategiesPreserveBulkAndEmptyStaleWrites(t *testing.T) {
	client, db := protectionTestDatabase(t)
	repo := newAccountRepositoryWithSQL(client, db, nil)
	svc := service.NewAntiDegradeService(protectionIntegrationStore{repo})
	ctx := context.Background()
	for _, mode := range service.RegisteredProtectionModes() {
		t.Run(mode, func(t *testing.T) {
			a := &service.Account{Name: mode, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Concurrency: 32, Status: service.StatusActive}
			require.NoError(t, repo.Create(ctx, a))
			before, err := svc.ApplyAntiDegradeMode(ctx, a.ID, service.AntiDegradeMode(mode))
			require.NoError(t, err)
			attack := map[string]any{"anti_degradation": false, "anti_degrade": nil, "codex_fingerprint_mode": "off", "enable_tls_fingerprint": false, "tls_fingerprint_builtin": nil}
			require.NoError(t, repo.UpdateExtra(ctx, a.ID, attack))
			conc := 999
			_, err = repo.BulkUpdate(ctx, []int64{a.ID}, service.AccountBulkUpdate{Concurrency: &conc, Extra: attack})
			require.NoError(t, err)
			stale := *before
			stale.Extra = nil
			stale.Concurrency = 999
			require.NoError(t, repo.Update(ctx, &stale))
			after, err := repo.GetByID(ctx, a.ID)
			require.NoError(t, err)
			expectedExtra := maps.Clone(before.Extra)
			expectedMarker := maps.Clone(before.Extra[service.AntiDegradeMarkerExtraKey].(map[string]any))
			expectedMarker["max_concurrency"] = float64(conc)
			expectedExtra[service.AntiDegradeMarkerExtraKey] = expectedMarker
			require.Equal(t, expectedExtra, after.Extra)
			require.Equal(t, conc, after.Concurrency)
		})
	}
}
