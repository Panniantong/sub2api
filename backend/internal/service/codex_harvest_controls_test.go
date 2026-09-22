package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type harvestControlSettingsRepo struct {
	SettingRepository
	mu  sync.Mutex
	raw string
	err error
}

func (r *harvestControlSettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if key != codexHarvestControlsKey || r.raw == "" && r.err == nil {
		return "", ErrSettingNotFound
	}
	return r.raw, r.err
}
func (r *harvestControlSettingsRepo) Set(_ context.Context, _ string, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.raw = value
	return nil
}

func TestHarvestControlsPersistAndRetainLastValidOnError(t *testing.T) {
	repo := &harvestControlSettingsRepo{}
	s := NewCodexHarvestService(nil, repo, nil)
	v, saved, err := s.Controls(context.Background())
	require.NoError(t, err)
	require.False(t, saved)
	require.False(t, v.NodeMemoryEnabled)
	v.NodeMemoryEnabled = true
	v.Speed = CodexHarvestSpeedPresets()["slow"]
	require.NoError(t, s.SaveControls(context.Background(), v))
	select {
	case <-s.wake:
	default:
		t.Fatal("settings did not wake the existing loop")
	}
	restarted := NewCodexHarvestService(nil, repo, nil)
	loaded, saved, err := restarted.Controls(context.Background())
	require.NoError(t, err)
	require.True(t, saved)
	require.Equal(t, v, loaded)
	repo.err = errors.New("storage failure")
	restarted.loadedUntil = time.Time{}
	loaded, saved, err = restarted.Controls(context.Background())
	require.Error(t, err)
	require.True(t, saved)
	require.Equal(t, v, loaded)
	v.Speed.MaxNodeAttempts = 11
	require.Error(t, s.SaveControls(context.Background(), v))
}

func TestHarvestControlsAllowAggressiveCadence(t *testing.T) {
	v := CodexHarvestControls{Version: 1, Speed: CodexHarvestSpeedPresets()["burst"]}
	require.NoError(t, ValidateCodexHarvestControls(v))
	require.Equal(t, 1, v.Speed.RoundIntervalSeconds)
	require.Zero(t, v.Speed.ProbeIntervalSeconds)
	require.Equal(t, 1, v.Speed.CooldownSeconds)
	v.Speed.RoundIntervalSeconds = 0
	require.Error(t, ValidateCodexHarvestControls(v))
	v.Speed.RoundIntervalSeconds = 1
	v.Speed.AttemptTimeoutSeconds = 0
	require.Error(t, ValidateCodexHarvestControls(v))
	bounds := CodexHarvestSpeedBounds()
	require.Equal(t, CodexHarvestBound{Min: 1, Max: 3600}, bounds["round_interval_seconds"])
	require.Equal(t, CodexHarvestBound{Min: 0, Max: 60}, bounds["probe_interval_seconds"])
	require.Equal(t, CodexHarvestBound{Min: 1, Max: 120}, bounds["attempt_timeout_seconds"])
	require.Equal(t, CodexHarvestBound{Min: 1, Max: 3600}, bounds["cooldown_seconds"])
	require.Equal(t, CodexHarvestBound{Min: 60, Max: 1800}, bounds["refresh_before_seconds"])
	require.Equal(t, 60, CodexHarvestSpeedPresets()["burst"].RefreshBeforeSeconds)
}

func TestHarvestControlsFillRefreshBeforeOnLegacyJSON(t *testing.T) {
	repo := &harvestControlSettingsRepo{raw: `{"version":1,"node_memory_enabled":true,"speed":{"round_interval_seconds":180,"probe_interval_seconds":2,"attempt_timeout_seconds":25,"cooldown_seconds":180,"max_requests_per_round":6,"max_node_attempts":3}}`}
	s := NewCodexHarvestService(nil, repo, nil)
	loaded, saved, err := s.Controls(context.Background())
	require.NoError(t, err)
	require.True(t, saved)
	require.Equal(t, 600, loaded.Speed.RefreshBeforeSeconds)
}

func TestHarvestActualRequestBudgetIsAtomic(t *testing.T) {
	round := &codexHarvestRound{limit: 6}
	var wg sync.WaitGroup
	var sent atomic.Int64
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if round.Take(100) {
				sent.Add(1)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 6, sent.Load())
	require.Equal(t, 6, round.Used())
	require.False(t, round.Take(3))
}

func TestHarvestProbeClassification(t *testing.T) {
	for _, tc := range []struct {
		name           string
		status, length int
		body, want     string
	}{
		{"good_personal", 200, 292, `{"status":"completed"}`, "success"},
		{"good_team", 200, 332, `{"status":"completed"}`, "success"},
		{"bad_shape_200", 200, 312, `{"status":"completed"}`, "invalid_state"},
		{"header_without_completion", 200, 292, ``, "response_incomplete_or_error"},
		{"unauthorized_json", 401, 0, `{"error":"expired"}`, "account_error"},
		{"rate_limit_json", 429, 0, `{"error":"limit"}`, "rate_limited"},
		{"upstream_error", 503, 0, `no service`, "upstream_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) {
				h := http.Header{}
				h.Set(openAICodexTurnStateHeader, fakeCodexTicketState(tc.length))
				h.Set("Retry-After", "300")
				return &http.Response{StatusCode: tc.status, Header: h, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			}}
			s := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true}, u)
			account := ticketTestAccount(1)
			if tc.length == 332 {
				account.Credentials["plan_type"] = "team"
			}
			r := s.executeCodexHarvestProbe(context.Background(), account, "fixture", "gpt-6-astra", "http://fixture.invalid", time.Second, nil, "")
			require.Equal(t, tc.want, r.Kind)
			require.Equal(t, tc.status, r.Status)
			require.Equal(t, 300*time.Second, r.RetryAfter)
		})
	}
}

func TestHarvestRejectedReservationDoesNotSend(t *testing.T) {
	var calls int
	u := &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) { calls++; return codexTicketResponse(), nil }}
	s := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true}, u)
	r := s.executeCodexHarvestProbe(context.Background(), ticketTestAccount(1), "fixture", "gpt-6-astra", "http://fixture.invalid", time.Second, func() bool { return false }, "")
	require.Zero(t, calls)
	require.False(t, r.Sent)
	require.Equal(t, "not_sent", r.Kind)
}
