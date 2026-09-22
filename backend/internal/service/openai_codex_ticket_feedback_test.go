package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
	"time"
)

func TestTicketStandbyFeedbackDoesNotRevokeReplacement(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, Models: []string{"gpt-6-astra"}}, nil)
	account := ticketTestAccount(41)
	makeTicket := func(state string) *openAICodexTicket {
		return &openAICodexTicket{Model: "gpt-6-astra", State: state, Length: 292, CapturedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	}
	primary := fakeCodexTicketState(292)
	backup := primary[:len(primary)-4] + "BBBB"
	require.NoError(t, svc.storeOpenAICodexTicket(context.Background(), account, makeTicket(primary)))
	require.NoError(t, svc.storeOpenAICodexTicket(context.Background(), account, makeTicket(backup)))
	got := svc.lookupOpenAICodexTicket(account, "gpt-6-astra")
	require.Equal(t, primary, got.State)
	require.Equal(t, backup, got.Standby.State)
	req, _ := http.NewRequest(http.MethodPost, "https://example.com", nil)
	req.Header.Set(openAICodexTurnStateHeader, primary)
	resp := &http.Response{StatusCode: 200, Header: http.Header{}}
	resp.Header.Set(openAICodexTurnStateHeader, "malformed")
	svc.observeCodexTicketResponse(req, resp, account)
	require.Equal(t, backup, svc.lookupOpenAICodexTicket(account, "gpt-6-astra").State)
	svc.observeCodexTicketResponse(req, resp, account)
	require.Equal(t, backup, svc.lookupOpenAICodexTicket(account, "gpt-6-astra").State)
	account.Credentials["chatgpt_account_id"] = "different-account"
	require.Nil(t, svc.lookupOpenAICodexTicket(account, "gpt-6-astra"))
}

func TestTicket429DoesNotInvalidate(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, Models: []string{"gpt-6-astra"}}, nil)
	account := ticketTestAccount(41)
	state := fakeCodexTicketState(292)
	require.NoError(t, svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{Model: "gpt-6-astra", State: state, Length: 292, ExpiresAt: time.Now().Add(time.Hour)}))
	req, _ := http.NewRequest(http.MethodPost, "https://example.com", nil)
	req.Header.Set(openAICodexTurnStateHeader, state)
	resp := &http.Response{StatusCode: 429, Header: http.Header{}}
	resp.Header.Set(openAICodexTurnStateHeader, "bad")
	svc.observeCodexTicketResponse(req, resp, account)
	require.True(t, svc.lookupOpenAICodexTicket(account, "gpt-6-astra").valid(time.Now(), 292))
}

func TestTicketStandbyRecoveredAfterRestart(t *testing.T) {
	account := ticketTestAccount(41)
	state := fakeCodexTicketState(292)
	account.Extra = map[string]any{openAICodexTicketExtraKey("gpt-6-astra"): &openAICodexTicket{
		State: state, Length: 292, ExpiresAt: time.Now().Add(-time.Second), Identity: ticketIdentity(account),
		Standby: &openAICodexTicket{State: state, Length: 292, Identity: ticketIdentity(account), ExpiresAt: time.Now().Add(time.Hour)},
	}}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true}, nil)
	require.True(t, svc.lookupOpenAICodexTicket(account, "gpt-6-astra").valid(time.Now(), 292))
	statuses := OpenAICodexTicketStatuses(account, config.OpenAICodexTicketConfig{Enabled: true, Models: []string{"gpt-6-astra"}}, time.Now())
	require.True(t, statuses[0].Ready)
}

func TestObserveCodexTicketResponseRotatesValidReturnedState(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TargetLength: 292, TTLSeconds: 3600, Models: []string{"gpt-6-astra"}}, nil)
	account := ticketTestAccount(41)
	primary := fakeCodexTicketStateSalt(292, 1)
	rotated := fakeCodexTicketStateSalt(292, 2)
	require.NotEqual(t, primary, rotated)
	require.NoError(t, svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
		Model: "gpt-6-astra", State: primary, Length: 292,
		HarvestSessionID: "harvest-session-sticky", HarvestNodeName: "london-02",
		CapturedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}))
	req, err := http.NewRequest(http.MethodPost, "https://example.com", nil)
	require.NoError(t, err)
	req.Header.Set(openAICodexTurnStateHeader, primary)
	resp := &http.Response{StatusCode: 200, Header: http.Header{}}
	resp.Header.Set(openAICodexTurnStateHeader, rotated)
	svc.observeCodexTicketResponse(req, resp, account)
	got := svc.lookupOpenAICodexTicket(account, "gpt-6-astra")
	require.NotNil(t, got)
	require.Equal(t, rotated, got.State)
	require.Equal(t, 292, got.Length)
	require.Equal(t, "harvest-session-sticky", got.HarvestSessionID)
	require.Equal(t, "london-02", got.HarvestNodeName)
	require.False(t, got.Revoked)
	require.True(t, got.valid(time.Now(), 292))
}

func TestObserveCodexTicketResponseSameStateKeepsTicket(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TargetLength: 292, TTLSeconds: 3600, Models: []string{"gpt-6-astra"}}, nil)
	account := ticketTestAccount(41)
	state := fakeCodexTicketState(292)
	require.NoError(t, svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
		Model: "gpt-6-astra", State: state, Length: 292,
		HarvestSessionID: "harvest-session-sticky",
		CapturedAt:       time.Unix(1700000000, 0), ExpiresAt: time.Now().Add(time.Hour),
	}))
	before := svc.lookupOpenAICodexTicket(account, "gpt-6-astra").CapturedAt
	req, err := http.NewRequest(http.MethodPost, "https://example.com", nil)
	require.NoError(t, err)
	req.Header.Set(openAICodexTurnStateHeader, state)
	resp := &http.Response{StatusCode: 200, Header: http.Header{}}
	resp.Header.Set(openAICodexTurnStateHeader, state)
	svc.observeCodexTicketResponse(req, resp, account)
	got := svc.lookupOpenAICodexTicket(account, "gpt-6-astra")
	require.Equal(t, state, got.State)
	require.Equal(t, before, got.CapturedAt)
	require.False(t, got.Revoked)
}
