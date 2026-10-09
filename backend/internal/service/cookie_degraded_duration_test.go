package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCookieDegradedDurationKeepsEpisodeAcrossRetries(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	account := cookieGuardTestAccount("", time.Time{})
	exhaustCookieTestCandidates(&account)
	repo := &cookieGuardBindingRepo{account: &account}
	settingsService := cookieDegradedTestSettings()
	settings, err := settingsService.GetOpenAICookieSettings(ctx)
	require.NoError(t, err)
	settings.rotationCandidates = func() ([]OpenAICodexCookieLibraryEntry, error) {
		return []OpenAICodexCookieLibraryEntry{{Host: "candidate.example", Cookie: "cookie", ExpiresAt: now.Add(time.Hour)}}, nil
	}
	svc := &OpenAIGatewayService{accountRepo: repo, settingService: settingsService}
	svc.syncCookieDegradedSince(ctx, &account, settings, now)
	require.Equal(t, now.Format(time.RFC3339Nano), account.GetExtraString(openAICookieDegradedSinceExtraKey))
	svc.setOpenAICookieRotationState(ctx, &account, "running", "retry")
	svc.syncCookieDegradedSince(ctx, &account, settings, now.Add(10*time.Minute))
	require.Equal(t, now, *cookieDegradedSince(&account, now.Add(10*time.Minute)))
	// A fresh account loaded after restart retains the persisted start.
	reloaded := cookieGuardTestAccount("", time.Time{})
	reloaded.Extra[openAICookieDegradedSinceExtraKey] = repo.updates[0][openAICookieDegradedSinceExtraKey]
	require.Equal(t, now, *cookieDegradedSince(&reloaded, now.Add(10*time.Minute)))
	// Storage failures do not look like recovery.
	settings.rotationCandidates = func() ([]OpenAICodexCookieLibraryEntry, error) { return nil, errors.New("offline") }
	svc.syncCookieDegradedSince(ctx, &account, settings, now.Add(11*time.Minute))
	require.Equal(t, now, *cookieDegradedSince(&account, now.Add(11*time.Minute)))
	settings.rotationCandidates = func() ([]OpenAICodexCookieLibraryEntry, error) {
		return []OpenAICodexCookieLibraryEntry{{Host: "new.example", Cookie: "cookie", ExpiresAt: now.Add(time.Hour)}}, nil
	}
	svc.syncCookieDegradedSince(ctx, &account, settings, now.Add(12*time.Minute))
	require.Empty(t, account.GetExtraString(openAICookieDegradedSinceExtraKey))
	settings.rotationCandidates = func() ([]OpenAICodexCookieLibraryEntry, error) {
		return []OpenAICodexCookieLibraryEntry{{Host: "candidate.example", Cookie: "cookie", ExpiresAt: now.Add(time.Hour)}}, nil
	}
	svc.syncCookieDegradedSince(ctx, &account, settings, now.Add(13*time.Minute))
	require.Equal(t, now.Add(13*time.Minute), *cookieDegradedSince(&account, now.Add(14*time.Minute)))
}

func TestCookieDegradedDurationExpiryAndRecovery(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	expired := now.Add(-10 * time.Minute)
	a := cookieGuardTestAccount("old.example", expired)
	exhaustCookieTestCandidates(&a)
	settings := cookieDegradedTestSettings()
	status := settings.OpenAICookieBinding(ctx, &a)
	require.Equal(t, int64(3), status.DegradedGroupID)
	require.Equal(t, expired, *status.DegradedSince)
	a.Extra[openAICookieDegradedSinceExtraKey] = now.Add(-time.Hour).Format(time.RFC3339Nano)
	require.Equal(t, expired, *cookieDegradedSince(&a, now), "a newer binding starts a new episode")
	repo := &cookieGuardBindingRepo{account: &a}
	svc := &OpenAIGatewayService{accountRepo: repo, settingService: settings}
	require.NoError(t, svc.bindOpenAICodexCookieHost(ctx, &a, "new.example"))
	require.Empty(t, a.GetExtraString(openAICookieDegradedSinceExtraKey))
	status = settings.OpenAICookieBinding(ctx, &a)
	require.Zero(t, status.DegradedGroupID)
	require.Nil(t, status.DegradedSince)
}

func TestCookieDegradedDurationFailedSaveDoesNotPublishStart(t *testing.T) {
	a := cookieGuardTestAccount("", time.Time{})
	svc := &OpenAIGatewayService{accountRepo: &cookieGuardBindingRepo{err: errors.New("offline")}}
	settings := &OpenAICookieSettings{RotationGroupIDs: []int64{2}, DegradedGroupID: 3,
		rotationCandidates: func() ([]OpenAICodexCookieLibraryEntry, error) { return nil, nil }}
	svc.syncCookieDegradedSince(context.Background(), &a, settings, time.Now())
	require.Empty(t, a.GetExtraString(openAICookieDegradedSinceExtraKey))
}
