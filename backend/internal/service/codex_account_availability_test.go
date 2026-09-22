package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func TestResolveCodexAccountAvailability(t *testing.T) {
	now := time.Date(2026, 9, 20, 22, 50, 0, 0, time.Local)
	at := func(d time.Duration) *time.Time {
		v := now.Add(d)
		return &v
	}
	expired := now.Add(-time.Hour)

	cases := []struct {
		name      string
		account   Account
		wantKind  string
		wantRecov time.Duration
	}{
		{name: "available", account: Account{Status: StatusActive, Schedulable: true}, wantKind: CodexAccountAvailabilityAvailable},
		{name: "rate limited while schedulable", account: Account{Status: StatusActive, Schedulable: true, RateLimitResetAt: at(2 * time.Hour)}, wantKind: CodexAccountAvailabilityRateLimited, wantRecov: 2 * time.Hour},
		{name: "rate limit expired", account: Account{Status: StatusActive, Schedulable: true, RateLimitResetAt: at(-time.Minute)}, wantKind: CodexAccountAvailabilityAvailable},
		{name: "credential error", account: Account{Status: "error", Schedulable: false}, wantKind: CodexAccountAvailabilityError},
		{name: "error beats schedulable", account: Account{Status: "error", Schedulable: true}, wantKind: CodexAccountAvailabilityError},
		{name: "manually disabled", account: Account{Status: StatusActive, Schedulable: false}, wantKind: CodexAccountAvailabilityDisabled},
		{name: "expired auto pause", account: Account{Status: StatusActive, Schedulable: true, ExpiresAt: &expired, AutoPauseOnExpired: true}, wantKind: CodexAccountAvailabilityExpired},
		{name: "expired without auto pause", account: Account{Status: StatusActive, Schedulable: true, ExpiresAt: &expired, AutoPauseOnExpired: false}, wantKind: CodexAccountAvailabilityAvailable},
		{name: "overload", account: Account{Status: StatusActive, Schedulable: true, OverloadUntil: at(15 * time.Minute)}, wantKind: CodexAccountAvailabilityOverload, wantRecov: 15 * time.Minute},
		{name: "temp unschedulable", account: Account{Status: StatusActive, Schedulable: true, TempUnschedulableUntil: at(50 * time.Minute)}, wantKind: CodexAccountAvailabilityTempUnschedulable, wantRecov: 50 * time.Minute},
		{name: "latest window wins rate limit", account: Account{Status: StatusActive, Schedulable: true, TempUnschedulableUntil: at(50 * time.Minute), RateLimitResetAt: at(2 * time.Hour)}, wantKind: CodexAccountAvailabilityRateLimited, wantRecov: 2 * time.Hour},
		{name: "latest window wins overload", account: Account{Status: StatusActive, Schedulable: true, RateLimitResetAt: at(10 * time.Minute), OverloadUntil: at(4 * time.Hour)}, wantKind: CodexAccountAvailabilityOverload, wantRecov: 4 * time.Hour},
		{name: "nil account", wantKind: CodexAccountAvailabilityError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ptr *Account
			if tc.name != "nil account" {
				a := tc.account
				ptr = &a
			}
			kind, recoverAt := ResolveCodexAccountAvailability(ptr, now)
			if kind != tc.wantKind {
				t.Fatalf("kind = %q, want %q", kind, tc.wantKind)
			}
			if tc.wantRecov == 0 {
				if recoverAt != nil {
					t.Fatalf("recoverAt = %v, want nil", recoverAt)
				}
				return
			}
			if recoverAt == nil || !recoverAt.Equal(now.Add(tc.wantRecov)) {
				t.Fatalf("recoverAt = %v, want %v", recoverAt, now.Add(tc.wantRecov))
			}
		})
	}
}

func TestDescribeCodexProbeFailure(t *testing.T) {
	cases := []struct {
		raw        string
		status     int
		wantLevel  string
		wantSubstr string
	}{
		{"invalid probe event", 200, "WARN", "不是有效数据"},
		{"probe response failed", 200, "WARN", "上游明确返回失败"},
		{"unterminated probe event", 200, "WARN", "被中途截断"},
		{`Post "https://chatgpt.com/...": context deadline exceeded`, 0, "WARN", "响应超时"},
		{"unexpected EOF", 0, "WARN", "连接被中断"},
		{"dial tcp: lookup x: no such host", 0, "WARN", "连不上"},
		{"", 401, "ERROR", "凭证已失效"},
		{"", 403, "WARN", "上游拒绝"},
		{"completely unknown", 200, "WARN", "没有拿到合规门票"},
	}
	for _, tc := range cases {
		message, level, detail := describeCodexProbeFailure(tc.raw, tc.status, "gpt-6-astra", "node-abc")
		if level != tc.wantLevel {
			t.Errorf("%q/%d level = %q, want %q", tc.raw, tc.status, level, tc.wantLevel)
		}
		if !strings.Contains(message, tc.wantSubstr) {
			t.Errorf("%q/%d message = %q, want substring %q", tc.raw, tc.status, message, tc.wantSubstr)
		}
		if tc.raw != "" && !strings.Contains(detail, tc.raw) {
			t.Errorf("%q/%d detail = %q, want original error", tc.raw, tc.status, detail)
		}
		if !strings.Contains(detail, "model=gpt-6-astra") || !strings.Contains(detail, "node=node-abc") {
			t.Errorf("%q/%d detail = %q, want model and node", tc.raw, tc.status, detail)
		}
	}
}

func TestManualHarvestShouldSwitch(t *testing.T) {
	cases := []struct {
		rule  string
		fails int
		kind  string
		len   int
		blk   int
		want  bool
	}{
		{"never", 9, "invalid_state", 312, 11, false},
		{"every_request", 0, "success", 292, 10, true},
		{"312_only", 0, "invalid_state", 312, 11, true},
		{"312_only", 2, "network_error", 0, 0, false},
		{"312_or_2fail", 2, "network_error", 0, 0, true},
		{"312_or_2fail", 1, "network_error", 0, 0, false},
		{"312_or_2fail", 0, "invalid_state", 356, 13, true},
	}
	for _, tc := range cases {
		got := manualHarvestShouldSwitch(tc.rule, tc.fails, tc.kind, tc.len, tc.blk)
		if got != tc.want {
			t.Errorf("%s fails=%d kind=%s -> %v, want %v", tc.rule, tc.fails, tc.kind, got, tc.want)
		}
	}
}

func TestNormalizeManualHarvestRequest(t *testing.T) {
	got, err := NormalizeManualHarvestRequest(ManualHarvestRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeSwitchRule != ManualHarvestNodeSwitch312Or2Fail || got.ProbeIntervalSeconds != 10 || got.MaxAttempts != 20 {
		t.Fatalf("defaults = %+v", got)
	}
	if _, err := NormalizeManualHarvestRequest(ManualHarvestRequest{NodeSwitchRule: "nope"}); err == nil {
		t.Fatal("expected invalid node_switch_rule")
	}
	if _, err := NormalizeManualHarvestRequest(ManualHarvestRequest{ProbeIntervalSeconds: 400}); err == nil {
		t.Fatal("expected probe interval error")
	}
}

func TestManualHarvestRunComplete(t *testing.T) {
	models := []string{"gpt-6-astra", "gpt-5.6-sol"}
	got := map[string]struct{}{}
	if manualHarvestRunComplete(false, models, got) || manualHarvestRunComplete(true, models, got) {
		t.Fatal("empty run must continue")
	}
	got["gpt-6-astra"] = struct{}{}
	if !manualHarvestRunComplete(true, models, got) {
		t.Fatal("stop_on_success should halt after first hit")
	}
	if manualHarvestRunComplete(false, models, got) {
		t.Fatal("both-model run must continue until sol hits")
	}
	if !manualHarvestModelDone(got, "gpt-6-astra") || manualHarvestModelDone(got, "gpt-5.6-sol") {
		t.Fatal("only astra should be marked done")
	}
	got["gpt-5.6-sol"] = struct{}{}
	if !manualHarvestRunComplete(false, models, got) {
		t.Fatal("both-model run should stop after every model hits")
	}
}

func TestManualHarvestLiveModels(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled: true, TargetLength: 292, TTLSeconds: 3600,
		Models: []string{"gpt-6-astra", "gpt-5.6-sol"},
	}, nil)
	account := ticketTestAccount(2)
	models := []string{"gpt-6-astra", "gpt-5.6-sol"}
	if len(svc.manualHarvestLiveModels(account, models)) != 0 {
		t.Fatal("no tickets yet")
	}
	if err := svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
		AccountID: 2, Model: "gpt-6-astra", State: fakeCodexTicketState(292), Length: 292, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	got := svc.manualHarvestLiveModels(account, models)
	if !manualHarvestModelDone(got, "gpt-6-astra") || manualHarvestModelDone(got, "gpt-5.6-sol") {
		t.Fatalf("got=%v", got)
	}
	if !manualHarvestRunComplete(true, models, got) {
		t.Fatal("stop_on_success should halt on existing astra ticket")
	}
	if manualHarvestRunComplete(false, models, got) {
		t.Fatal("both-model run must still harvest sol")
	}
}

func TestCodexTicketChatHold(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TargetLength: 292}, nil)
	account := ticketTestAccount(2)
	if svc.codexTicketChatHeld(2) {
		t.Fatal("idle")
	}
	first := svc.holdCodexTicketChat(account)
	second := svc.holdCodexTicketChat(account)
	if !svc.codexTicketChatHeld(2) {
		t.Fatal("held")
	}
	first()
	if !svc.codexTicketChatHeld(2) {
		t.Fatal("nested hold")
	}
	second()
	if svc.codexTicketChatHeld(2) {
		t.Fatal("released")
	}
}

func TestDescribeCodexHarvestOutcome(t *testing.T) {
	msg, level, _ := describeCodexHarvestOutcome("success", "", 200, 292, 10, 292, 10, "gpt-6-astra", "node-a")
	if level != "OK" || !strings.Contains(msg, "成功捕获合规门票") {
		t.Fatalf("success: level=%s msg=%s", level, msg)
	}
	msg, level, detail := describeCodexHarvestOutcome("invalid_state", "", 200, 312, 11, 292, 10, "gpt-6-astra", "node-a")
	if level != "WARN" || !strings.Contains(msg, "降智票据") || !strings.Contains(detail, "292/10") {
		t.Fatalf("degraded: level=%s msg=%s detail=%s", level, msg, detail)
	}
}
