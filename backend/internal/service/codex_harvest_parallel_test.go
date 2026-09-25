//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type fakeHarvestCollection struct {
	next   atomic.Int64
	closed atomic.Bool
}

func (f *fakeHarvestCollection) Next(ctx context.Context, lane int) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	n := f.next.Add(1)
	return fmt.Sprintf("node-%d", n), fmt.Sprintf("http://127.0.0.1:%d", 17893+lane), nil
}
func (f *fakeHarvestCollection) Close() error { f.closed.Store(true); return nil }

func TestParallelHarvestBudgetAndFailedPersistence(t *testing.T) {
	for _, kind := range []string{"miss", "persist_failed", "hit"} {
		t.Run(kind, func(t *testing.T) {
			account := ticketTestAccount(41)
			var requests atomic.Int64
			svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TTLSeconds: 3600, Models: []string{"gpt-6-astra"}}, &codexTicketFuncUpstream{do: func(req *http.Request) (*http.Response, error) {
				requests.Add(1)
				resp := codexTicketResponse()
				if kind == "miss" {
					resp.Header.Set(openAICodexTurnStateHeader, fakeCodexTicketState(312))
				}
				return resp, nil
			}})
			svc.accountRepo = &manualHarvestAccountRepo{account: account, persist: func(context.Context) error {
				if kind == "persist_failed" {
					return errors.New("secret database error")
				}
				return nil
			}}
			collection := &fakeHarvestCollection{}
			var events []ManualHarvestProgress
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err := svc.runParallelHarvest(ctx, ManualHarvestRequest{CollectLanes: 3, MaxAttempts: 7, Models: []string{"gpt-6-astra"}, StopOnSuccess: true}, account, func(p ManualHarvestProgress) { events = append(events, p) }, collection)
			require.NoError(t, err)
			require.True(t, collection.closed.Load())
			require.LessOrEqual(t, requests.Load(), int64(7))
			require.NotEmpty(t, events)
			last := events[len(events)-1]
			require.True(t, last.Done)
			if kind == "hit" {
				require.Equal(t, 1, last.TicketsStored)
				ticket := svc.lookupOpenAICodexTicket(account, "gpt-6-astra")
				require.Equal(t, "managed", ticket.HarvestNodeProvider)
				require.NotEmpty(t, ticket.HarvestSessionID)
			} else {
				require.Zero(t, last.TicketsStored)
				for _, e := range events {
					require.NotEqual(t, "hit", e.Result)
					require.NotContains(t, e.Message, "secret")
				}
				if kind == "miss" {
					require.EqualValues(t, 7, requests.Load())
				}
			}
		})
	}
}

func TestParallelHarvestCancelsOtherProbesAfterPersistedWinner(t *testing.T) {
	account := ticketTestAccount(41)
	var started atomic.Int64
	var cancelled atomic.Int64
	ready := make(chan struct{})
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TTLSeconds: 3600, Models: []string{"gpt-6-astra"}}, &codexTicketFuncUpstream{do: func(req *http.Request) (*http.Response, error) {
		n := started.Add(1)
		if n == 3 {
			close(ready)
		}
		select {
		case <-ready:
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
		if n == 1 {
			return codexTicketResponse(), nil
		}
		<-req.Context().Done()
		cancelled.Add(1)
		return nil, req.Context().Err()
	}})
	svc.accountRepo = &manualHarvestAccountRepo{account: account, persist: func(context.Context) error { return nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := svc.runParallelHarvest(ctx, ManualHarvestRequest{CollectLanes: 3, MaxAttempts: 10, Models: []string{"gpt-6-astra"}, StopOnSuccess: true}, account, func(ManualHarvestProgress) {}, &fakeHarvestCollection{})
	require.NoError(t, err)
	require.EqualValues(t, 3, started.Load())
	require.EqualValues(t, 2, cancelled.Load())
}

func TestParallelProbeBudgetIsAtomic(t *testing.T) {
	var used atomic.Int64
	var wg sync.WaitGroup
	var admitted atomic.Int64
	for range 64 {
		wg.Go(func() {
			if reserveParallelProbe(&used, 7) > 0 {
				admitted.Add(1)
			}
		})
	}
	wg.Wait()
	require.EqualValues(t, 7, used.Load())
	require.EqualValues(t, 7, admitted.Load())
}

func TestParallelRequestValidationAndMutualExclusion(t *testing.T) {
	_, err := NormalizeManualHarvestRequest(ManualHarvestRequest{CollectLanes: 33})
	require.Error(t, err)
	svc := &OpenAIGatewayService{accountRepo: &manualHarvestAccountRepo{}}
	svc.codexHarvestRunMu.Lock()
	defer svc.codexHarvestRunMu.Unlock()
	err = svc.ExecuteManualHarvest(context.Background(), ManualHarvestRequest{CollectLanes: 10}, nil)
	require.EqualError(t, err, "another harvest is running")
}

func TestParallelHarvestKeepsSafeRouteDiagnostics(t *testing.T) {
	resetCodexHarvestFlow()
	t.Cleanup(resetCodexHarvestFlow)
	account := ticketTestAccount(41)
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TargetLength: 780}, &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) {
		resp := codexTicketResponse()
		resp.Header.Set(openAICodexTurnStateHeader, mint780State(time.Now()))
		return resp, nil
	}})
	svc.accountRepo = &manualHarvestAccountRepo{account: account}
	var progress []ManualHarvestProgress
	err := svc.runParallelHarvest(context.Background(), ManualHarvestRequest{CollectLanes: 2, MaxAttempts: 2, Models: []string{"gpt-6-astra"}}, account, func(p ManualHarvestProgress) { progress = append(progress, p) }, &fakeHarvestCollection{})
	require.NoError(t, err)
	misses := 0
	for _, p := range progress {
		if p.Result == "invalid_route" {
			misses++
			require.Equal(t, 780, p.Length)
			require.Contains(t, p.Detail, "route_pair_missing")
			require.Zero(t, p.TicketsStored)
		}
	}
	require.Equal(t, 2, misses)
	for _, event := range listCodexHarvestFlowEvents() {
		if event.Stage == "probe" {
			require.Contains(t, event.Detail, "route_pair_missing")
		}
	}
}
