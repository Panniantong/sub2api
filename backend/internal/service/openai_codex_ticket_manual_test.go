//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// manualHarvestAccountRepo only implements GetByID, which is all the manual
// harvest entry point needs before it starts probing.
type manualHarvestAccountRepo struct {
	AccountRepository
	account *Account
	persist func(context.Context) error
}

func TestManualHarvestBudgetIsSharedAcrossModels(t *testing.T) {
	account := ticketTestAccount(41)
	calls := 0
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{TTLSeconds: 3600}, &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) {
		calls++
		resp := codexTicketResponse()
		resp.Header.Set(openAICodexTurnStateHeader, fakeCodexTicketState(312))
		return resp, nil
	}})
	svc.accountRepo = &manualHarvestAccountRepo{account: account}
	var events []ManualHarvestProgress
	err := svc.ExecuteManualHarvest(context.Background(), ManualHarvestRequest{AccountID: 41, Models: []string{"gpt-6-astra", "gpt-5.6-sol"}, MaxAttempts: 1, ProbeIntervalSeconds: 1, NodeSwitchRule: ManualHarvestNodeSwitchEveryRequest}, func(p ManualHarvestProgress) { events = append(events, p) })
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	for _, event := range events {
		require.NotContains(t, event.Message, "已切换到节点")
		require.Empty(t, event.Node)
	}
}

func (r *manualHarvestAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func (r *manualHarvestAccountRepo) UpdateExtra(ctx context.Context, _ int64, _ map[string]any) error {
	return r.persist(ctx)
}

func TestManualHarvestPersistenceOutcome(t *testing.T) {
	for _, scenario := range []string{"success", "failure", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			resetCodexHarvestFlow()
			t.Cleanup(resetCodexHarvestFlow)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			account := ticketTestAccount(41)
			calls := 0
			svc := ticketTestService(t, config.OpenAICodexTicketConfig{TTLSeconds: 3600}, &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) {
				calls++
				return codexTicketResponse(), nil
			}})
			svc.accountRepo = &manualHarvestAccountRepo{account: account, persist: func(ctx context.Context) error {
				if scenario == "cancelled" {
					cancel()
					return ctx.Err()
				}
				if scenario == "failure" {
					return errors.New("database password=secret unavailable")
				}
				return nil
			}}
			var events []ManualHarvestProgress
			err := svc.ExecuteManualHarvest(ctx, ManualHarvestRequest{AccountID: 41, Models: []string{"gpt-6-astra"}, StopOnSuccess: true}, func(p ManualHarvestProgress) {
				events = append(events, p)
				if p.Result == "persist_failed" {
					cancel()
				}
			})
			require.Equal(t, 1, calls)
			require.NotNil(t, svc.lookupOpenAICodexTicket(account, "gpt-6-astra"), "memory capture is retained")
			if scenario == "success" {
				require.NoError(t, err)
				require.True(t, events[len(events)-1].Done)
				require.Equal(t, 1, events[len(events)-1].TicketsStored)
			} else {
				for _, event := range listCodexHarvestFlowEvents() {
					require.NotEqual(t, "accept", event.Kind, "failed persistence must not emit a stored event")
				}
				require.ErrorIs(t, err, context.Canceled)
				for _, event := range events {
					require.Zero(t, event.TicketsStored)
					require.NotEqual(t, "hit", event.Result)
					require.NotContains(t, event.Message, "secret")
					require.False(t, event.Done)
				}
				if scenario == "failure" {
					found := false
					for _, event := range events {
						if event.Result == "persist_failed" {
							found = true
						}
					}
					require.True(t, found)
				}
			}
		})
	}
}

func TestManualHarvestPersistenceRetryDoesNotStopOnCapture(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	account := ticketTestAccount(41)
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{TTLSeconds: 3600}, &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) { return codexTicketResponse(), nil }})
	writes := 0
	svc.accountRepo = &manualHarvestAccountRepo{account: account, persist: func(context.Context) error {
		writes++
		if writes == 1 {
			return errors.New("write failed")
		}
		return nil
	}}
	var final ManualHarvestProgress
	err := svc.ExecuteManualHarvest(ctx, ManualHarvestRequest{AccountID: 41, Models: []string{"gpt-6-astra"}, ProbeIntervalSeconds: 1, MaxAttempts: 2, StopOnSuccess: true}, func(p ManualHarvestProgress) { final = p })
	require.NoError(t, err)
	require.Equal(t, 2, writes)
	require.True(t, final.Done)
	require.Equal(t, 1, final.TicketsStored)
}

// A manual harvest sends the account's access token to the ChatGPT ticket
// endpoint, so accounts that can never hold a Codex ticket must be rejected
// before any upstream request is attempted.
func TestExecuteManualHarvestRejectsUnsupportedAccounts(t *testing.T) {
	for name, mutate := range map[string]func(*Account){
		"anthropic-platform": func(a *Account) { a.Platform = PlatformAnthropic },
		"api-key-type":       func(a *Account) { a.Type = AccountTypeAPIKey },
		"shadow-account":     func(a *Account) { parent := int64(7); a.ParentAccountID = &parent },
	} {
		t.Run(name, func(t *testing.T) {
			account := ticketTestAccount(41)
			mutate(account)
			svc := ticketTestService(t, config.OpenAICodexTicketConfig{HarvestAttemptTimeoutSeconds: 25, TTLSeconds: 3600}, nil)
			svc.accountRepo = &manualHarvestAccountRepo{account: account}

			err := svc.ExecuteManualHarvest(context.Background(), ManualHarvestRequest{AccountID: 41}, func(ManualHarvestProgress) {})
			require.Error(t, err)
			require.Contains(t, err.Error(), "does not support Codex ticket harvesting")
		})
	}
}

// The admin API is reachable without the UI form, so out-of-range values must be
// clamped server-side instead of letting one request hammer upstream for hours.
func TestNormalizeManualHarvestRequestClampsBounds(t *testing.T) {
	req := ManualHarvestRequest{
		Models:                   []string{" gpt-6-astra ", "", "gpt-6-astra", "gpt-5.6-sol"},
		ProbeIntervalSeconds:     1_000_000,
		RateLimitCooldownSeconds: 9_999,
		MaxAttempts:              100_000,
	}
	normalizeManualHarvestRequest(&req)
	require.Equal(t, manualHarvestProbeIntervalMax, req.ProbeIntervalSeconds)
	require.Equal(t, manualHarvestRateLimitCooldownMax, req.RateLimitCooldownSeconds)
	require.Equal(t, manualHarvestMaxAttemptsMax, req.MaxAttempts)
	require.Equal(t, []string{"gpt-6-astra", "gpt-5.6-sol"}, req.Models)

	// Zero values mean unset and fall back to the documented defaults rather
	// than being clamped to the minimum.
	defaults := ManualHarvestRequest{}
	normalizeManualHarvestRequest(&defaults)
	require.Equal(t, 10, defaults.ProbeIntervalSeconds)
	require.Equal(t, 30, defaults.RateLimitCooldownSeconds)
	require.Equal(t, 20, defaults.MaxAttempts)
	require.Equal(t, []string{openAICodexTicketDefaultModel, openAICodexTicketDefaultSolModel}, defaults.Models)

	// Explicitly negative values are clamped to the lower bound.
	negative := ManualHarvestRequest{ProbeIntervalSeconds: -5, RateLimitCooldownSeconds: -1, MaxAttempts: -3}
	normalizeManualHarvestRequest(&negative)
	require.Equal(t, manualHarvestProbeIntervalMin, negative.ProbeIntervalSeconds)
	require.Equal(t, manualHarvestRateLimitCooldownMin, negative.RateLimitCooldownSeconds)
	require.Equal(t, manualHarvestMaxAttemptsMin, negative.MaxAttempts)
}

func TestNormalizeManualHarvestRequestCapsModelList(t *testing.T) {
	models := make([]string, 0, manualHarvestMaxModels+5)
	for i := 0; i < manualHarvestMaxModels+5; i++ {
		models = append(models, "model-"+string(rune('a'+i)))
	}
	req := ManualHarvestRequest{Models: models}
	normalizeManualHarvestRequest(&req)
	require.Len(t, req.Models, manualHarvestMaxModels)
}
