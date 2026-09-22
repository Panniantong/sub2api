package service

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type turnAdmissionRepo struct {
	AccountRepository
	account, parent *Account
	err             error
	reads           int
}

func (r *turnAdmissionRepo) GetOpenAITurnAdmission(context.Context, int64) (*Account, *Account, error) {
	r.reads++
	return r.account, r.parent, r.err
}

func TestOpenAITurnAdmissionLatestState(t *testing.T) {
	for _, change := range []string{"disabled", "paused", "expired", "removed_group", "deleted", "db_error", "binding", "credentials", "credential_route", "platform", "cooldown", "model_block", "persisted_model_limit", "fingerprint", "ws_mode", "proxy_endpoint"} {
		t.Run(change, func(t *testing.T) {
			selected := ticketTestAccount(901)
			selected.GroupIDs = []int64{9}
			latest := *selected
			latest.Extra = maps.Clone(selected.Extra)
			if latest.Extra == nil {
				latest.Extra = make(map[string]any)
			}
			repo := &turnAdmissionRepo{account: &latest}
			s := &OpenAIGatewayService{accountRepo: repo}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			group := int64(9)
			c.Set("api_key", &APIKey{GroupID: &group})
			switch change {
			case "disabled":
				latest.Status = "disabled"
			case "paused":
				latest.Schedulable = false
			case "expired":
				past := time.Now().Add(-time.Second)
				latest.ExpiresAt, latest.AutoPauseOnExpired = &past, true
			case "removed_group":
				latest.GroupIDs = []int64{10}
			case "deleted":
				repo.account = nil
			case "db_error":
				repo.err = errors.New("unavailable")
			case "binding":
				latest.ParentAccountID = func() *int64 {
					id := int64(902)
					return &id
				}()
			case "credentials":
				latest.Credentials = maps.Clone(selected.Credentials)
				latest.Credentials["access_token"] = "different"
				latest.Credentials["_token_version"] = int64(2)
			case "credential_route":
				latest.Credentials = map[string]any{"base_url": "https://changed.example.invalid"}
			case "platform":
				latest.Platform = PlatformKimi
			case "cooldown":
				s.openaiAccountRuntimeBlockUntil.Store(latest.ID, time.Now().Add(time.Minute))
			case "model_block":
				s.recordOpenAIAccountModelTransientFailure(&latest, "gpt-6-astra", time.Now())
				s.recordOpenAIAccountModelTransientFailure(&latest, "gpt-6-astra", time.Now())
			case "persisted_model_limit":
				latest.Extra[modelRateLimitsKey] = map[string]any{
					"gpt-6-astra": map[string]any{"rate_limit_reset_at": time.Now().Add(time.Hour).Format(time.RFC3339)},
				}
			case "fingerprint":
				latest.Extra[codexFingerprintSeedExtraKey] = "changed-synthetic-seed"
			case "ws_mode":
				latest.Extra["openai_oauth_responses_websockets_v2_mode"] = OpenAIWSIngressModeOff
			case "proxy_endpoint":
				id := int64(22)
				latest.ProxyID, selected.ProxyID = &id, &id
				selected.Proxy = &Proxy{ID: id, Protocol: "http", Host: "old.invalid", Port: 80}
				latest.Proxy = &Proxy{ID: id, Protocol: "http", Host: "new.invalid", Port: 80}
			}
			_, err := s.admitOpenAITurn(context.Background(), c, selected, "gpt-6-astra")
			if change == "credentials" {
				require.NoError(t, err)
			} else {
				require.True(t, IsOpenAITurnAdmissionError(err), "%v", err)
			}
			require.Equal(t, 1, repo.reads)
			require.True(t, selected.Schedulable)
			require.Nil(t, selected.TempUnschedulableUntil)
			if err != nil {
				require.Same(t, err, s.handleOpenAIUpstreamTransportError(context.Background(), c, selected, err, false))
			}
		})
	}
}

func TestOpenAITurnAdmissionExplicitGroupRejectsRemovedMembership(t *testing.T) {
	selected := ticketTestAccount(905)
	selected.GroupIDs = []int64{9}
	latest := *selected
	latest.GroupIDs = []int64{10}
	repo := &turnAdmissionRepo{account: &latest}
	s := &OpenAIGatewayService{accountRepo: repo}

	_, err := s.admitOpenAITurnForGroup(context.Background(), 9, selected, "gpt-5.5")

	require.True(t, IsOpenAITurnAdmissionError(err), "%v", err)
	var denied *OpenAITurnAdmissionError
	require.ErrorAs(t, err, &denied)
	require.Equal(t, "group_membership_changed", denied.Reason)
	require.Equal(t, 1, repo.reads)
}

func TestOpenAITurnAdmissionSimpleModeDoesNotRequireGroupMembership(t *testing.T) {
	selected := ticketTestAccount(906)
	selected.GroupIDs = []int64{9}
	latest := *selected
	latest.GroupIDs = nil
	repo := &turnAdmissionRepo{account: &latest}
	s := &OpenAIGatewayService{
		accountRepo: repo,
		cfg:         &config.Config{RunMode: config.RunModeSimple},
	}

	groupID := int64(9)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("api_key", &APIKey{GroupID: &groupID})

	got, err := s.admitOpenAITurn(context.Background(), c, selected, "gpt-5.5")

	require.NoError(t, err)
	require.Same(t, &latest, got)
	require.Equal(t, 1, repo.reads)
}

type turnAdmissionDialerFunc func(context.Context, string, http.Header, string) (openAIWSClientConn, int, http.Header, error)

func (f turnAdmissionDialerFunc) Dial(ctx context.Context, url string, headers http.Header, proxy string) (openAIWSClientConn, int, http.Header, error) {
	return f(ctx, url, headers, proxy)
}

func TestOpenAITurnAdmissionAfterDialBeforeLease(t *testing.T) {
	selected := ticketTestAccount(7)
	latest := *selected
	repo := &turnAdmissionRepo{account: &latest}
	s := &OpenAIGatewayService{accountRepo: repo}
	pool := newOpenAIWSConnPool(&config.Config{})
	defer pool.Close()
	upstream := newStagedPassthroughConn()
	pool.setClientDialerForTest(turnAdmissionDialerFunc(func(context.Context, string, http.Header, string) (openAIWSClientConn, int, http.Header, error) {
		// Simulate an admin update after the pre-dial read, not an actual DB mutation.
		latest.Schedulable = false
		return upstream, http.StatusSwitchingProtocols, http.Header{}, nil
	}))
	lease, err := pool.Acquire(context.Background(), openAIWSAcquireRequest{
		Account: selected, WSURL: "ws://loopback.invalid/responses",
		HeadersFactory: func(ctx context.Context, headers http.Header) (http.Header, error) {
			_, err := s.admitOpenAITurn(ctx, nil, selected, "gpt-5.5")
			return headers, err
		},
		BindHandshake: func(h http.Header) *openAIWSTurnBinding {
			return s.bindOpenAIWSHandshake(selected, "gpt-5.5", h)
		},
		CheckBinding: func(ctx context.Context, b *openAIWSTurnBinding) error {
			a, err := s.admitOpenAITurn(ctx, nil, selected, "gpt-5.5")
			if err != nil {
				return err
			}
			return s.checkOpenAIWSBinding(a, "gpt-5.5", b)
		},
	})
	require.True(t, IsOpenAITurnAdmissionError(err), "%v", err)
	require.Nil(t, lease)
	require.Empty(t, upstream.writes)
	select {
	case <-upstream.closed:
	default:
		t.Fatal("rejected lease was not released/closed")
	}
}

func TestOpenAITurnAdmissionRequiresPrimaryReader(t *testing.T) {
	s := &OpenAIGatewayService{requireLatestTurnAdmission: true}
	_, err := s.admitOpenAITurn(context.Background(), nil, ticketTestAccount(1), "gpt-5.5")
	require.True(t, IsOpenAITurnAdmissionError(err))
	s.accountRepo = &turnAdmissionRepo{account: ticketTestAccount(1)}
	_, err = s.admitOpenAITurn(context.Background(), nil, ticketTestAccount(1), "gpt-5.5")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.admitOpenAITurn(ctx, nil, ticketTestAccount(1), "gpt-5.5")
	require.ErrorIs(t, err, context.Canceled)
}

func TestOpenAITurnAdmissionReaderChecksLifecycle(t *testing.T) {
	for _, name := range []string{"disabled", "paused", "expired", "cooldown"} {
		t.Run(name, func(t *testing.T) {
			account := ticketTestAccount(17)
			switch name {
			case "disabled":
				account.Status = StatusDisabled
			case "paused":
				account.Schedulable = false
			case "expired":
				expired := time.Now().Add(-time.Second)
				account.AutoPauseOnExpired = true
				account.ExpiresAt = &expired
			case "cooldown":
				until := time.Now().Add(time.Minute)
				account.TempUnschedulableUntil = &until
			}
			s := &OpenAIGatewayService{accountRepo: &turnAdmissionRepo{account: account}}
			_, err := s.AdmitOpenAITurn(context.Background(), nil, account, "gpt-5.5")
			require.True(t, IsOpenAITurnAdmissionError(err), "%v", err)
		})
	}
}

func TestOpenAITurnAdmissionFailureInvalidatesOnlyCurrentSessionState(t *testing.T) {
	const groupID int64 = 41
	store := NewOpenAIWSStateStore(nil)
	s := &OpenAIGatewayService{openaiWSStateStore: store}

	require.NoError(t, store.BindResponseAccount(context.Background(), groupID, "resp_rejected", 901, time.Hour))
	store.BindResponseConn("resp_rejected", "conn-rejected", time.Hour)
	store.BindSessionTurnState(groupID, "session-rejected", "turn-state", time.Hour)
	store.BindSessionConn(groupID, "session-rejected", "conn-rejected", time.Hour)

	require.NoError(t, store.BindResponseAccount(context.Background(), groupID, "resp-unrelated", 902, time.Hour))
	store.BindResponseConn("resp-unrelated", "conn-unrelated", time.Hour)
	store.BindSessionTurnState(groupID, "session-unrelated", "turn-state", time.Hour)
	store.BindSessionConn(groupID, "session-unrelated", "conn-unrelated", time.Hour)

	s.invalidateOpenAIWSTurnStateAfterAdmissionFailure(
		context.Background(),
		groupID,
		"session-rejected",
		"resp_rejected",
		901,
		&OpenAITurnAdmissionError{Reason: "account_ineligible"},
	)

	accountID, err := store.GetResponseAccount(context.Background(), groupID, "resp_rejected")
	require.NoError(t, err)
	require.Zero(t, accountID)
	_, responseConnExists := store.GetResponseConn("resp_rejected")
	require.False(t, responseConnExists)
	_, turnStateExists := store.GetSessionTurnState(groupID, "session-rejected")
	require.False(t, turnStateExists)
	_, sessionConnExists := store.GetSessionConn(groupID, "session-rejected")
	require.False(t, sessionConnExists)

	unrelatedAccountID, err := store.GetResponseAccount(context.Background(), groupID, "resp-unrelated")
	require.NoError(t, err)
	require.Equal(t, int64(902), unrelatedAccountID)
	unrelatedConn, ok := store.GetResponseConn("resp-unrelated")
	require.True(t, ok)
	require.Equal(t, "conn-unrelated", unrelatedConn)
	unrelatedState, ok := store.GetSessionTurnState(groupID, "session-unrelated")
	require.True(t, ok)
	require.Equal(t, "turn-state", unrelatedState)
	unrelatedSessionConn, ok := store.GetSessionConn(groupID, "session-unrelated")
	require.True(t, ok)
	require.Equal(t, "conn-unrelated", unrelatedSessionConn)
}

func TestOpenAITurnAdmissionFailureDoesNotClearStateOnControlPlaneReadError(t *testing.T) {
	const groupID int64 = 42
	store := NewOpenAIWSStateStore(nil)
	s := &OpenAIGatewayService{openaiWSStateStore: store}
	require.NoError(t, store.BindResponseAccount(context.Background(), groupID, "resp-read-error", 903, time.Hour))
	store.BindSessionConn(groupID, "session-read-error", "conn-read-error", time.Hour)

	s.invalidateOpenAIWSTurnStateAfterAdmissionFailure(
		context.Background(),
		groupID,
		"session-read-error",
		"resp-read-error",
		903,
		&OpenAITurnAdmissionError{Reason: "latest_state_unavailable"},
	)

	accountID, err := store.GetResponseAccount(context.Background(), groupID, "resp-read-error")
	require.NoError(t, err)
	require.Equal(t, int64(903), accountID)
	connID, ok := store.GetSessionConn(groupID, "session-read-error")
	require.True(t, ok)
	require.Equal(t, "conn-read-error", connID)
}

func TestOpenAITurnAdmissionFailureCleanupUnwrapsClientCloseError(t *testing.T) {
	const groupID int64 = 43
	const sessionHash = "session-wrapped-admission"
	const responseID = "resp-wrapped-admission"
	store := NewOpenAIWSStateStore(nil)
	s := &OpenAIGatewayService{openaiWSStateStore: store}

	require.NoError(t, store.BindResponseAccount(context.Background(), groupID, responseID, 904, time.Hour))
	store.BindResponseConn(responseID, "conn-wrapped-admission", time.Hour)
	store.BindSessionTurnState(groupID, sessionHash, "turn-state-wrapped", time.Hour)
	store.BindSessionConn(groupID, sessionHash, "conn-wrapped-admission", time.Hour)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(openAIWSIngressSessionHashContextKey, sessionHash)
	group := groupID
	c.Set("api_key", &APIKey{GroupID: &group})
	wrapped := NewOpenAIWSClientCloseError(
		coderws.StatusTryAgainLater,
		"account eligibility changed",
		&OpenAITurnAdmissionError{Reason: "account_ineligible"},
	)
	s.invalidateOpenAIWSTurnStateAfterAdmissionFailureForRequest(
		context.Background(),
		c,
		[]byte(`{"type":"response.create","previous_response_id":"resp-wrapped-admission"}`),
		904,
		wrapped,
	)

	accountID, err := store.GetResponseAccount(context.Background(), groupID, responseID)
	require.NoError(t, err)
	require.Zero(t, accountID)
	_, responseConnExists := store.GetResponseConn(responseID)
	require.False(t, responseConnExists)
	_, turnStateExists := store.GetSessionTurnState(groupID, sessionHash)
	require.False(t, turnStateExists)
	_, sessionConnExists := store.GetSessionConn(groupID, sessionHash)
	require.False(t, sessionConnExists)
}

func TestOpenAITurnAdmissionShadowParent(t *testing.T) {
	a := ticketTestAccount(1)
	parent := ticketTestAccount(2)
	a.ParentAccountID = &parent.ID
	parent.Schedulable = false
	future := time.Now().Add(time.Hour)
	parent.RateLimitResetAt = &future
	parent.OverloadUntil = &future
	repo := &turnAdmissionRepo{account: a, parent: parent}
	s := &OpenAIGatewayService{accountRepo: repo}
	_, err := s.admitOpenAITurn(context.Background(), nil, a, "gpt-5.3-codex-spark")
	require.NoError(t, err, "global parent quota must not pause the independent shadow")
	parent.TempUnschedulableUntil = &future
	_, err = s.admitOpenAITurn(context.Background(), nil, a, "gpt-5.3-codex-spark")
	require.True(t, IsOpenAITurnAdmissionError(err))
}

func TestOpenAITurnAdmissionModelAndHandshakeBinding(t *testing.T) {
	a := ticketTestAccount(1)
	attachReadyCodexTicket(a, "gpt-5.6-sol")
	s := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}, nil)
	h := http.Header{}
	require.NoError(t, s.applyOpenAICodexTicket(context.Background(), a, "gpt-5.6-sol", h))
	b := s.bindOpenAIWSHandshake(a, "gpt-5.6-sol", h)
	_, err := s.admitOpenAITurn(context.Background(), nil, a, "gpt-6-astra")
	require.ErrorIs(t, err, ErrOpenAICodexTicketUnavailable)
	_, err = s.admitOpenAITurn(context.Background(), nil, a, "gpt-5.6-sol")
	require.NoError(t, err)
	require.NoError(t, s.checkOpenAIWSBinding(a, "gpt-5.6-sol", b))
	primary := *s.lookupOpenAICodexTicket(a, "gpt-5.6-sol")
	standby := primary
	standby.State = fakeCodexTicketState(292)[:291] + "A"
	primary.Standby = &standby
	s.openaiCodexTickets.Store(openAICodexTicketKey(a.ID, "gpt-5.6-sol"), &primary)
	require.NoError(t, s.checkOpenAIWSBinding(a, "gpt-5.6-sol", b), "standby refresh must not force reconnection")
	// A valid inventory does not extend a connection's original handshake.
	b.ticket.ExpiresAt = time.Now().Add(-time.Second)
	require.Error(t, s.checkOpenAIWSBinding(a, "gpt-5.6-sol", b))
	require.True(t, a.Schedulable)
	require.Nil(t, a.TempUnschedulableUntil)
}

func TestOpenAITurnAdmissionMissingHandshakeTicketFailsClosed(t *testing.T) {
	a := ticketTestAccount(2)
	s := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled:    true,
		FailClosed: true,
		Models:     []string{"gpt-6-astra"},
	}, nil)
	binding := &openAIWSTurnBinding{
		model:       "gpt-6-astra",
		fingerprint: openAITurnRouteFingerprint(a),
		createdAt:   time.Now(),
	}

	err := s.checkOpenAIWSBinding(a, "gpt-6-astra", binding)

	var denied *OpenAITurnAdmissionError
	require.ErrorAs(t, err, &denied)
	require.Equal(t, "connection_ticket_missing", denied.Reason)
}

func TestOpenAITurnAdmissionBindingUsesFinalHeaders(t *testing.T) {
	a := ticketTestAccount(1)
	attachReadyCodexTicket(a, "gpt-6-astra")
	a.Extra[codexFingerprintSeedExtraKey] = testCodexFingerprintSeed
	a.Extra[codexFingerprintModeExtraKey] = string(codexFingerprintFull)
	s := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}, nil)
	pool := newOpenAIWSConnPool(&config.Config{})
	defer pool.Close()
	upstream := newStagedPassthroughConn()
	pool.setClientDialerForTest(&stagedPassthroughDialer{conn: upstream})
	req := openAIWSAcquireRequest{
		Account: a, WSURL: "ws://loopback.invalid/responses", Headers: http.Header{},
		HeadersFactory: func(ctx context.Context, h http.Header) (http.Header, error) {
			h.Set("session_id", "factory-session")
			return h, s.applyOpenAICodexTicket(ctx, a, "gpt-6-astra", h)
		},
		BindHandshake: func(h http.Header) *openAIWSTurnBinding {
			return s.bindOpenAIWSHandshake(a, "gpt-6-astra", h)
		},
	}
	conn, err := pool.dialConn(context.Background(), req)
	require.NoError(t, err)
	defer conn.close()
	require.NotNil(t, conn.turnBinding.ticket)
	require.Equal(t, "factory-session", conn.handshakeCompatibility.sessionIDUnderscore)
	require.NoError(t, s.checkOpenAIWSBinding(a, "gpt-6-astra", conn.turnBinding))
}
