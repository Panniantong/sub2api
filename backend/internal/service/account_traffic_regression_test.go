package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountTrafficTerminalFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		payload string
		status  int
	}{
		{`{"type":"response.done","response":{"status":"failed","error":{"code":"rate_limit_exceeded"}}}`, 429},
		{`{"type":"response.done","response":{"status":"failed","status_details":{"error":{"type":"server_error"}}}}`, 503},
		{`{"type":"response.failed","response":{"error":{"status_code":502}}}`, 502},
		{`{"type":"response.done","response":{"status":"cancelled"}}`, 499},
		{`{"type":"response.done","response":{"status":"completed"}}`, 200},
		{`{"type":"response.failed","response":{"error":{"code":"invalid_request"}}}`, 499},
	} {
		status, terminal := accountTrafficEventStatus([]byte(tc.payload))
		require.True(t, terminal)
		require.Equal(t, tc.status, status, tc.payload)
		body := &accountTrafficBody{}
		body.inspect([]byte("data: " + tc.payload + "\n\ndata: [DONE]\n\n"))
		require.EqualValues(t, tc.status, body.terminalStatus.Load(), "SSE must preserve the same terminal classification")
	}
}

type staleObservationCache struct{ AccountTrafficCache }

func (staleObservationCache) Acquire(context.Context, AccountTrafficPlan, string) (AccountTrafficAdmission, error) {
	return AccountTrafficAdmission{Allowed: true, SkipObservation: true}, nil
}
func TestAccountTrafficConfirmedObservationChangeDoesNotBlockOrCreateLease(t *testing.T) {
	p := DefaultAccountTrafficPolicy()
	p.AdaptiveEnabled = true
	ctx, permit, err := NewAccountTrafficService(staleObservationCache{}).Begin(context.Background(), AccountTrafficPlan{AccountID: 42, Policy: p})
	require.NoError(t, err)
	require.Nil(t, permit)
	require.NoError(t, ctx.Err())
}

func TestAccountTrafficGrokRealtimeRejectsUnsupportedHardControlsBeforeDial(t *testing.T) {
	for _, controls := range []map[string]any{{"strict_rpm_enabled": true}, {"adaptive_enabled": true, "adaptive_mode": "automatic"}} {
		a := &Account{ID: 42, Platform: PlatformGrok, Type: AccountTypeAPIKey, Concurrency: 8, Extra: map[string]any{AccountTrafficPolicyKey: controls}}
		gateway := &OpenAIGatewayService{}
		_, err := gateway.OpenGrokRealtime(context.Background(), a, "unused-test-token", "unused-model")
		var local *UpstreamFailoverError
		require.ErrorAs(t, err, &local)
		require.Equal(t, http.StatusBadRequest, local.StatusCode)
		require.False(t, local.ShouldReportAccountScheduleFailure())
		require.Contains(t, local.ClientMessage, "逐轮硬限制")
	}
	a := &Account{ID: 42, Platform: PlatformGrok, Concurrency: 8, Extra: map[string]any{AccountTrafficPolicyKey: map[string]any{"adaptive_enabled": true}}}
	require.NoError(t, validateGrokRealtimeTrafficPolicy(a))
}
