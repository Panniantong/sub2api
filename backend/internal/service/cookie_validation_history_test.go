package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

type cookieHistoryRepoStub struct {
	*cookieTestRepo
	entries []OpenAICookieAcquisitionLog
}

func (r *cookieHistoryRepoStub) AppendCookieValidation(_ context.Context, item OpenAICookieAcquisitionLog) error {
	r.entries = append(r.entries, item)
	return nil
}
func (r *cookieHistoryRepoStub) CookieValidationPage(context.Context, int64, string, int, int) ([]OpenAICookieAcquisitionLog, int64, error) {
	return r.entries, int64(len(r.entries)), nil
}
func (r *cookieHistoryRepoStub) CookieValidationHosts(context.Context, int64) ([]string, error) {
	return []string{"host.example"}, nil
}

func TestCookieValidationHistoryDoesNotEvictBinding(t *testing.T) {
	r := &cookieHistoryRepoStub{cookieTestRepo: &cookieTestRepo{values: map[string]string{}}}
	s := &SettingService{settingRepo: r}
	ctx := context.Background()
	require.NoError(t, s.appendOpenAICookieLog(ctx, OpenAICookieAcquisitionLog{Kind: "validation", ID: "binding", Stage: "host_bound", AccountID: 1}))
	for i := 0; i < 1100; i++ {
		require.NoError(t, s.appendOpenAICookieLog(ctx, OpenAICookieAcquisitionLog{Kind: "validation", Stage: "validation_rejected", AccountID: 2}))
	}
	require.Len(t, r.entries, 1101)
	require.Equal(t, "binding", r.entries[0].ID)
	for _, status := range []string{"rotation_waiting", "rotation_started", "rotation_candidate"} {
		require.NoError(t, s.appendOpenAICookieLog(ctx, OpenAICookieAcquisitionLog{Kind: "validation", BindingStatus: status}))
	}
	require.Len(t, r.entries, 1101)
	require.Empty(t, r.values, "durable history bypasses the global JSON array")
}

func TestCookieValidationSerializesAndRejectsStaleBinding(t *testing.T) {
	a := cookieGuardTestAccount("old.example", time.Now().Add(-time.Minute))
	fresh := cookieGuardTestAccount("new.example", time.Now().Add(time.Hour))
	fresh.Extra[openAICodexCookieRotationNextExtraKey] = time.Now().Add(time.Minute).Format(time.RFC3339Nano)
	s := &OpenAIGatewayService{accountRepo: &cookieGuardBindingRepo{account: &fresh}}
	lock := &sync.Mutex{}
	s.openaiCookieRotationLocks.Store(a.ID, lock)
	lock.Lock()
	result, err := s.validateBindAndBuildOpenAICookieHost(context.Background(), &a, nil, "candidate.example", "cookie", "", "", "")
	require.NoError(t, err)
	require.Equal(t, "busy", result)
	lock.Unlock()
	result, err = s.validateBindAndBuildOpenAICookieHost(context.Background(), &a, nil, "candidate.example", "cookie", "", "", "")
	require.NoError(t, err)
	require.Equal(t, "already_bound", result)
	require.Equal(t, "new.example", openAICodexCookieHostFromAccount(&a))
}
