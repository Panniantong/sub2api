package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type harvestLearningMemory struct {
	CodexHarvestNodeRepository
	mu         sync.Mutex
	generation int64
	feedback   []CodexHarvestNodeFeedback
}

func (m *harvestLearningMemory) Snapshot(_ context.Context, _ CodexHarvestNodeScope) (int64, []CodexHarvestNodeRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := []CodexHarvestNodeRecord{}
	for _, f := range m.feedback {
		if f.Result == "success" {
			now := time.Now()
			items = append(items, CodexHarvestNodeRecord{NodeID: f.Node.ID, Successes: 1, LastSuccess: &now})
		}
	}
	return m.generation, items, nil
}
func (m *harvestLearningMemory) Record(_ context.Context, f CodexHarvestNodeFeedback) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != f.Generation {
		return false, nil
	}
	m.feedback = append(m.feedback, f)
	return true, nil
}
func (m *harvestLearningMemory) Reset(_ context.Context, _ int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.generation++
	m.feedback = nil
	return nil
}

type harvestFreshRepo struct {
	AccountRepository
	account Account
}

func (r *harvestFreshRepo) GetByID(context.Context, int64) (*Account, error) {
	a := r.account
	return &a, nil
}
func (r *harvestFreshRepo) ListByPlatform(context.Context, string) ([]Account, error) {
	return []Account{r.account}, nil
}
func (r *harvestFreshRepo) UpdateExtra(context.Context, int64, map[string]any) error { return nil }

func learningHarvestFixture(t *testing.T, budget int) (*OpenAIGatewayService, *harvestLearningMemory, func() string) {
	t.Helper()
	var mu sync.Mutex
	selected := ""
	ctrl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPut {
			var body map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			selected = body["name"]
			w.WriteHeader(204)
			return
		}
		group := map[string]any{"type": "Selector", "now": selected, "all": []string{"p-US", "p-JP", "p-SG"}}
		switch r.URL.Path {
		case "/providers/proxies":
			leaves := make([]map[string]any, 0, 3)
			for _, name := range []string{"p-US", "p-JP", "p-SG"} {
				leaves = append(leaves, map[string]any{"name": name, "type": "Shadowsocks", "id": name + "-runtime", "provider-name": "airport"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"providers": map[string]any{
				"airport": map[string]any{"name": "airport", "vehicleType": "HTTP", "proxies": leaves},
			}})
		case "/proxies":
			_ = json.NewEncoder(w).Encode(map[string]any{"proxies": map[string]any{"CODEX-HARVEST-SELECT": group}})
		case "/proxies/CODEX-HARVEST-SELECT":
			_ = json.NewEncoder(w).Encode(group)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ctrl.Close)
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)
	root := filepath.Join(dir, "mihomo-codex")
	require.NoError(t, os.Mkdir(root, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "provider.yaml"), []byte("proxies:\n - {name: US, type: ss, server: us.example.com, port: 443}\n - {name: JP, type: ss, server: jp.example.com, port: 443}\n - {name: SG, type: ss, server: sg.example.com, port: 443}\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte("mixed-port: 3101\nexternal-controller: "+ctrl.URL+"\nsecret: fixture\nlisteners:\n - {name: codex-harvest-directed, type: mixed, listen: 127.0.0.1, port: 3102, proxy: CODEX-HARVEST-SELECT}\nproxy-providers:\n airport:\n  path: ./provider.yaml\n  override: {additional-prefix: p-}\n"), 0600))
	cfg := config.OpenAICodexTicketConfig{Enabled: true, Models: []string{"gpt-6-astra"}, HarvestProxyURL: "http://127.0.0.1:3101", MaxProbesPerRound: budget}
	s := ticketTestService(t, cfg, nil)
	settings := &harvestControlSettingsRepo{}
	memory := &harvestLearningMemory{generation: 1}
	s.codexHarvest = NewCodexHarvestService(memory, settings, s.cfg)
	s.codexHarvest.current.NodeMemoryEnabled = true
	s.codexHarvest.loadedUntil = time.Now().Add(time.Hour)
	// Keep the clock out of routing tests; pacing has its own cancellation test.
	s.codexHarvest.configured = false
	s.settingService = NewSettingService(settings, s.cfg)
	s.accountRepo = &harvestFreshRepo{account: *ticketTestAccount(1)}
	return s, memory, func() string { mu.Lock(); defer mu.Unlock(); return selected }
}

func TestHarvestDirectedRetriesRespectActualBudget(t *testing.T) {
	for _, tc := range []struct{ budget, successOn, want int }{{2, 3, 2}, {6, 3, 3}, {6, 99, 3}} {
		t.Run(string(rune('0'+tc.budget))+"-"+string(rune('0'+tc.want)), func(t *testing.T) {
			s, memory, node := learningHarvestFixture(t, tc.budget)
			seen := map[string]bool{}
			calls := 0
			s.httpUpstream = &codexTicketFuncUpstream{do: func(req *http.Request) (*http.Response, error) {
				calls++
				selected := node()
				require.NotEmpty(t, selected, "directed probing must select an identifiable provider node")
				require.False(t, seen[selected], "node repeated in one hunt")
				seen[selected] = true
				response := codexTicketResponse()
				if calls != tc.successOn {
					response.Header.Set(openAICodexTurnStateHeader, fakeCodexTicketState(312))
				}
				return response, nil
			}}
			s.probeOnceOpenAICodexTicket(context.Background(), ticketTestAccount(1), "gpt-6-astra")
			require.Equal(t, tc.want, calls)
			require.Len(t, memory.feedback, tc.want)
			require.Equal(t, "invalid_state", memory.feedback[0].Result)
			if tc.successOn <= tc.want {
				require.Equal(t, "success", memory.feedback[tc.want-1].Result)
			}
		})
	}
}

func TestHarvestResetDuringProbeDoesNotRepopulateLearning(t *testing.T) {
	s, memory, _ := learningHarvestFixture(t, 6)
	s.httpUpstream = &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) {
		require.NoError(t, s.codexHarvest.ResetNodes(context.Background(), 0))
		return codexTicketResponse(), nil
	}}
	s.probeOnceOpenAICodexTicket(context.Background(), ticketTestAccount(1), "gpt-6-astra")
	require.Empty(t, memory.feedback)
	require.True(t, s.lookupOpenAICodexTicket(ticketTestAccount(1), "gpt-6-astra").valid(time.Now(), 292))
}

func TestHarvestAccountErrorsStopRetriesAndHonorRetryAfter(t *testing.T) {
	for _, status := range []int{401, 429} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s, memory, _ := learningHarvestFixture(t, 6)
			calls := 0
			s.httpUpstream = &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) {
				calls++
				r := codexTicketResponse()
				r.StatusCode = status
				r.Header.Set("Retry-After", "600")
				return r, nil
			}}
			s.probeOnceOpenAICodexTicket(context.Background(), ticketTestAccount(1), "gpt-6-astra")
			require.Equal(t, 1, calls)
			require.Len(t, memory.feedback, 1)
			until, ok := s.openaiCodexTicketProbeCooldown.Load(openAICodexTicketKey(1, "gpt-6-astra"))
			require.True(t, ok)
			untilTime, ok := until.(time.Time)
			require.True(t, ok)
			require.True(t, untilTime.After(time.Now().Add(590*time.Second)))
		})
	}
}

func TestHarvestSuccessfulNodePreferredNextRound(t *testing.T) {
	s, _, node := learningHarvestFixture(t, 6)
	var selected []string
	s.httpUpstream = &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) {
		selected = append(selected, node())
		return codexTicketResponse(), nil
	}}
	for range 2 {
		s.openaiCodexTickets.Delete(openAICodexTicketKey(1, "gpt-6-astra"))
		s.probeOnceOpenAICodexTicket(context.Background(), ticketTestAccount(1), "gpt-6-astra")
	}
	require.Len(t, selected, 2)
	require.Equal(t, selected[0], selected[1])
}

func TestHarvestRechecksSkipImmediatelyBeforeRequest(t *testing.T) {
	s, _, _ := learningHarvestFixture(t, 6)
	repo, ok := s.accountRepo.(*harvestFreshRepo)
	require.True(t, ok)
	repo.account.Extra = map[string]any{OpenAICodexSkipHarvestExtraKey: true}
	calls := 0
	s.httpUpstream = &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) { calls++; return codexTicketResponse(), nil }}
	s.probeOnceOpenAICodexTicket(context.Background(), ticketTestAccount(1), "gpt-6-astra")
	require.Zero(t, calls)
}

func TestHarvestSkipsWhenChatHeld(t *testing.T) {
	s, _, _ := learningHarvestFixture(t, 6)
	account := ticketTestAccount(1)
	release := s.holdCodexTicketChat(account)
	defer release()
	calls := 0
	s.httpUpstream = &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) { calls++; return codexTicketResponse(), nil }}
	s.probeOnceOpenAICodexTicket(context.Background(), account, "gpt-6-astra")
	require.Zero(t, calls)
}

func TestHarvestAccountErrorStopsOtherModelsInRound(t *testing.T) {
	for _, status := range []int{401, 429} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s, _, _ := learningHarvestFixture(t, 6)
			s.cfg.Gateway.OpenAICodexTicket.Models = []string{"gpt-6-astra", "gpt-5.6-sol"}
			calls := 0
			s.httpUpstream = &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) {
				calls++
				r := codexTicketResponse()
				r.StatusCode = status
				return r, nil
			}}
			s.refreshOpenAICodexTickets(context.Background())
			require.Equal(t, 1, calls)
		})
	}
}

func TestHarvestCancellationReleasesLeaseWithoutPenalty(t *testing.T) {
	s, memory, _ := learningHarvestFixture(t, 6)
	ctx, cancel := context.WithCancel(context.Background())
	s.httpUpstream = &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) {
		cancel()
		return nil, context.Canceled
	}}
	s.probeOnceOpenAICodexTicket(ctx, ticketTestAccount(1), "gpt-6-astra")
	require.Empty(t, memory.feedback)
	require.False(t, s.ticketProbeCoolingDown(1, "gpt-6-astra", time.Now()))
	s.httpUpstream = &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) { return codexTicketResponse(), nil }}
	second, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	s.probeOnceOpenAICodexTicket(second, ticketTestAccount(1), "gpt-6-astra")
	require.Len(t, memory.feedback, 1)
	require.Equal(t, "success", memory.feedback[0].Result)
}

func TestHarvestControllerFallbackStillUsesRoundBudget(t *testing.T) {
	s, memory, _ := learningHarvestFixture(t, 1)
	t.Setenv("DATA_DIR", t.TempDir())
	s.cfg.Gateway.OpenAICodexTicket.Models = []string{"gpt-6-astra", "gpt-5.6-sol"}
	calls := 0
	s.httpUpstream = &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) {
		calls++
		return codexTicketResponse(), nil
	}}
	s.refreshOpenAICodexTickets(context.Background())
	require.Equal(t, 1, calls)
	require.Empty(t, memory.feedback)
	require.NotEmpty(t, s.codexHarvest.Runtime().DegradedReason)
}

func TestHarvestDisabledMemoryPreservesLearningRecords(t *testing.T) {
	s, memory, selected := learningHarvestFixture(t, 6)
	s.codexHarvest.current.NodeMemoryEnabled = false
	memory.feedback = []CodexHarvestNodeFeedback{{Result: "success"}}
	s.httpUpstream = &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) { return codexTicketResponse(), nil }}
	s.probeOnceOpenAICodexTicket(context.Background(), ticketTestAccount(1), "gpt-6-astra")
	require.Empty(t, selected())
	require.Len(t, memory.feedback, 1)
	require.True(t, s.lookupOpenAICodexTicket(ticketTestAccount(1), "gpt-6-astra").valid(time.Now(), 292))
}

func TestHarvestReservationRechecksControlsAndIdentity(t *testing.T) {
	for _, change := range []string{"disable", "skip", "identity"} {
		t.Run(change, func(t *testing.T) {
			s, _, _ := learningHarvestFixture(t, 6)
			round := &codexHarvestRound{limit: 6}
			repo, ok := s.accountRepo.(*harvestFreshRepo)
			require.True(t, ok)
			switch change {
			case "disable":
				s.codexHarvest.current.NodeMemoryEnabled = false
			case "skip":
				repo.account.Extra = map[string]any{OpenAICodexSkipHarvestExtraKey: true}
			case "identity":
				repo.account.Credentials["chatgpt_account_id"] = "new-account"
			}
			require.False(t, s.reserveHarvestRequest(context.Background(), ticketTestAccount(1), "gpt-6-astra", round, true))
			require.Zero(t, round.Used())
		})
	}
}

func TestHarvestPaceCancellationDoesNotWaitFullGap(t *testing.T) {
	s, _, _ := learningHarvestFixture(t, 6)
	s.codexHarvest.lastRequest = time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v := s.codexHarvest.current
	v.Speed.ProbeIntervalSeconds = 60
	require.False(t, s.waitHarvestPace(ctx, v, true))
}

func TestHarvestPaceUsesLatestInterval(t *testing.T) {
	s, _, _ := learningHarvestFixture(t, 6)
	s.codexHarvest.configured = true
	s.codexHarvest.current.Speed.ProbeIntervalSeconds = 60
	s.codexHarvest.lastRequest = time.Now()
	done := make(chan bool, 1)
	go func() { done <- s.waitHarvestPace(context.Background(), s.codexHarvest.current, true) }()
	time.Sleep(80 * time.Millisecond)
	s.codexHarvest.configMu.Lock()
	s.codexHarvest.current.Speed.ProbeIntervalSeconds = 1
	s.codexHarvest.configMu.Unlock()
	select {
	case ok := <-done:
		require.True(t, ok)
	case <-time.After(2 * time.Second):
		t.Fatal("pace wait ignored updated interval")
	}
}

type harvestProxyUpstream struct {
	HTTPUpstream
	do func(*http.Request, string) (*http.Response, error)
}

func (u *harvestProxyUpstream) Do(req *http.Request, proxy string, _ int64, _ int) (*http.Response, error) {
	return u.do(req, proxy)
}

func TestHarvestRefreshPinsIssuingNodeAndSession(t *testing.T) {
	s, _, node := learningHarvestFixture(t, 6)
	var selected []string
	var sessions []string
	s.httpUpstream = &codexTicketFuncUpstream{do: func(req *http.Request) (*http.Response, error) {
		selected = append(selected, node())
		sessions = append(sessions, req.Header.Get("session_id"))
		return codexTicketResponse(), nil
	}}
	account := ticketTestAccount(1)
	s.probeOnceOpenAICodexTicket(context.Background(), account, "gpt-6-astra")
	ticket := s.lookupOpenAICodexTicket(account, "gpt-6-astra")
	require.NotNil(t, ticket)
	require.NotEmpty(t, ticket.HarvestNodeID)
	require.Equal(t, "http://127.0.0.1:3102", ticket.HarvestProxyURL)
	require.NotEmpty(t, ticket.HarvestSessionID)
	require.NoError(t, s.codexHarvest.ResetNodes(context.Background(), 0))
	ticket.ExpiresAt = time.Now().Add(time.Second)
	s.openaiCodexTicketProbeCooldown.Delete(openAICodexTicketKey(account.ID, "gpt-6-astra"))
	s.probeOnceOpenAICodexTicket(context.Background(), account, "gpt-6-astra")
	require.Len(t, selected, 2)
	require.Equal(t, selected[0], selected[1])
	require.NotEqual(t, sessions[0], sessions[1])
	require.NotEmpty(t, sessions[0])
	require.Equal(t, "ticket_sticky", s.codexHarvest.Runtime().SelectionReason)
}

func TestDoOpenAIUpstream_BoundTicketReacquiresHarvestNode(t *testing.T) {
	s, _, node := learningHarvestFixture(t, 6)
	s.httpUpstream = &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) {
		return codexTicketResponse(), nil
	}}
	account := ticketTestAccount(1)
	s.probeOnceOpenAICodexTicket(context.Background(), account, "gpt-6-astra")
	ticket := s.lookupOpenAICodexTicket(account, "gpt-6-astra")
	require.NotNil(t, ticket)
	require.NotEmpty(t, ticket.HarvestNodeID)
	var usedProxy, usedNode string
	s.httpUpstream = &harvestProxyUpstream{do: func(req *http.Request, proxy string) (*http.Response, error) {
		usedProxy = proxy
		usedNode = node()
		require.Equal(t, ticket.State, req.Header.Get(openAICodexTurnStateHeader))
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	}}
	req, err := http.NewRequest(http.MethodPost, "https://example.invalid/responses", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set(openAICodexTurnStateHeader, ticket.State)
	resp, err := s.doOpenAIUpstream(req, "http://account.example:8080", account)
	require.NoError(t, err)
	require.Equal(t, ticket.HarvestProxyURL, usedProxy)
	require.Equal(t, ticket.HarvestNodeName, usedNode)
	require.NoError(t, resp.Body.Close())
}
