package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type cookieFailureTestUpstream struct {
	HTTPUpstream
	status int
	body   string
	err    error
}

func (u cookieFailureTestUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	if u.err != nil {
		return nil, u.err
	}
	return &http.Response{StatusCode: u.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(u.body))}, nil
}

func TestCookieValidationFailureCooldown(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		answer  string
		err     error
		seconds int
		want    int
	}{
		{"http_401", 401, "", nil, 0, 120},
		{"http_400_default", 400, "invalid request", nil, 0, 120},
		{"http_400_custom", 400, "invalid request", nil, 300, 300},
		{"http_503", 503, "", nil, 0, 120},
		{"unexpected_answer", 200, "maybe", nil, 0, 120},
		{"timeout", 0, "", context.DeadlineExceeded, 0, 120},
		{"network", 0, "", errors.New("connection refused"), 300, 300},
		{"no_uses_existing_cooldown", 200, "no", nil, 120, 900},
		{"yes_has_no_cooldown", 200, "yes", nil, 120, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settingsService := cookieGuardTestSettings(t)
			settings, err := settingsService.GetOpenAICookieSettings(context.Background())
			require.NoError(t, err)
			settings.CookieHostValidationFailureCooldownSeconds = tc.seconds
			settings.WSHostCooldownSeconds = 900
			repo := &cookieGuardBindingRepo{}
			svc := &OpenAIGatewayService{settingService: settingsService, accountRepo: repo, httpUpstream: cookieFailureTestUpstream{status: tc.status, body: fmt.Sprintf(`{"output_text":%q}`, tc.answer), err: tc.err}}
			account := cookieGuardTestAccount("old.example", time.Now().Add(time.Minute))
			repo.account = &account
			start := time.Now()
			_, err = svc.validateBindAndBuildOpenAICookieHost(context.Background(), &account, settings, "candidate.example", "candidate=cookie", "", "ignored", "token")
			require.NoError(t, err)
			until := openAICodexCookieHostCooldownUntil(&account, "candidate.example")
			if tc.want == 0 {
				require.True(t, until.IsZero())
				return
			}
			require.WithinDuration(t, start.Add(time.Duration(tc.want)*time.Second), until, 2*time.Second)
			require.False(t, openAICookieRotationCandidate(&account, OpenAICodexCookieLibraryEntry{Host: "candidate.example", Cookie: "cookie", ExpiresAt: until.Add(time.Hour)}, time.Now()))
			require.True(t, openAICookieRotationCandidate(&account, OpenAICodexCookieLibraryEntry{Host: "candidate.example", Cookie: "cookie", ExpiresAt: until.Add(time.Hour)}, until.Add(time.Second)))
			require.Equal(t, "old.example", openAICodexCookieHostFromAccount(&account))
			require.NotEmpty(t, repo.updates)
			logs, err := settingsService.GetOpenAICookieValidationLogs(context.Background())
			require.NoError(t, err)
			require.Equal(t, "cooldown", logs[0].BindingStatus)
			if tc.answer != "no" {
				require.Contains(t, logs[0].Message, fmt.Sprintf("%d", tc.want))
			}
		})
	}
}

func TestCookieValidationCooldownSettings(t *testing.T) {
	s := cookieGuardTestSettings(t)
	v, err := s.GetOpenAICookieSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, 120, v.CookieHostValidationFailureCooldownSeconds)
	v.CookieHostValidationFailureCooldownSeconds = 300
	require.NoError(t, s.SetOpenAICookieSettings(context.Background(), v))
	loaded, err := (&SettingService{settingRepo: s.settingRepo}).GetOpenAICookieSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, 300, loaded.CookieHostValidationFailureCooldownSeconds)
	for _, invalid := range []int{-1, 604801} {
		v.CookieHostValidationFailureCooldownSeconds = invalid
		require.Error(t, s.SetOpenAICookieSettings(context.Background(), v))
	}
}

func TestCookieValidationModelRejectionUsesConfiguredCooldown(t *testing.T) {
	settingsService := cookieGuardTestSettings(t)
	settings, err := settingsService.GetOpenAICookieSettings(context.Background())
	require.NoError(t, err)
	settings.CookieHostValidationFailureCooldownSeconds = 300
	repo := &cookieGuardBindingRepo{}
	account := cookieGuardTestAccount("old.example", time.Now().Add(time.Minute))
	repo.account = &account
	svc := &OpenAIGatewayService{settingService: settingsService, accountRepo: repo, httpUpstream: cookieFailureTestUpstream{status: 400, body: `{"detail":"The 'gpt-6-astra' model is not supported when using Codex with a ChatGPT account."}`}}
	result, err := svc.validateBindAndBuildOpenAICookieHost(context.Background(), &account, settings, "candidate.example", "candidate=cookie", "", "ignored", "token")
	require.NoError(t, err)
	require.Equal(t, "error", result)
	require.WithinDuration(t, time.Now().Add(300*time.Second), openAICodexCookieHostCooldownUntil(&account, "candidate.example"), 2*time.Second)
	require.NotEmpty(t, repo.updates)
	logs, err := settingsService.GetOpenAICookieValidationLogs(context.Background())
	require.NoError(t, err)
	require.Contains(t, logs[0].Message, "model is not supported")
	require.Contains(t, logs[0].Message, "冷却 300 秒")
	require.Equal(t, "validation_failed", logs[0].Stage)
	require.Equal(t, "cooldown", logs[0].BindingStatus)
}

func TestCookieValidationUpstreamErrorSummary(t *testing.T) {
	require.Equal(t, "unsupported parameter", cookieValidationUpstreamError(`{"error":{"message":"unsupported parameter"}}`))
	require.Equal(t, "invalid request", cookieValidationUpstreamError("invalid\nrequest"))
	require.Contains(t, cookieValidationUpstreamError(strings.Repeat("错", 600)), "完整内容见 validation_response")
}
