package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

func TestSecurityPolicyBuiltinKeywordsQuality(t *testing.T) {
	seeds := SecurityPolicyBuiltinKeywords()
	if len(seeds) < 100 {
		t.Fatalf("builtin seeds = %d, want >= 100", len(seeds))
	}
	seen := make(map[string]string, len(seeds))
	categories := make(map[string]int)
	for _, seed := range seeds {
		if strings.TrimSpace(seed.Keyword) == "" {
			t.Fatal("builtin seed must not be blank")
		}
		key := strings.ToLower(strings.TrimSpace(seed.Keyword))
		if prev, dup := seen[key]; dup {
			t.Fatalf("duplicate builtin seed %q (categories %q vs %q)", seed.Keyword, prev, seed.Category)
		}
		seen[key] = seed.Category
		categories[seed.Category]++
	}
	for _, category := range []string{
		SecurityPolicyCategoryCrack, SecurityPolicyCategoryReverse,
		SecurityPolicyCategoryPentest, SecurityPolicyCategoryPrivesc,
		SecurityPolicyCategoryEvasion,
	} {
		if categories[category] == 0 {
			t.Fatalf("builtin seeds missing category %q", category)
		}
	}
}

func TestSecurityPolicySnapshotMatch(t *testing.T) {
	custom := []SecurityPolicyKeyword{
		{ID: 1, Keyword: "零日交易", Category: SecurityPolicyCategoryCustom},
		{ID: 2, GroupID: securityPolicyInt64Ptr(9), Keyword: "小组黑话", Category: SecurityPolicyCategoryCustom},
	}
	snapshot := BuildSecurityPolicySnapshot(custom)

	if keyword, category, ok := snapshot.Match(9, "请教我写免杀马过火绒"); !ok || keyword == "" {
		t.Fatalf("builtin hit expected, got %q %q %v", keyword, category, ok)
	} else if category != SecurityPolicyCategoryEvasion {
		t.Fatalf("category = %q, want evasion", category)
	}
	if keyword, _, ok := snapshot.Match(9, "哪里可以零日交易"); !ok || keyword != "零日交易" {
		t.Fatalf("global custom hit expected, got %q %v", keyword, ok)
	}
	if keyword, _, ok := snapshot.Match(9, "这是小组黑话测试"); !ok || keyword != "小组黑话" {
		t.Fatalf("group custom hit expected, got %q %v", keyword, ok)
	}
	if _, _, ok := snapshot.Match(7, "这是小组黑话测试"); ok {
		t.Fatal("group-scoped word must not match other groups")
	}
	if _, _, ok := snapshot.Match(9, "今天天气不错，适合出去散步"); ok {
		t.Fatal("benign text must not match")
	}
	// 内置与自定义同时命中同一文本时不 panic，结果二选一即可。
	snapshot.Match(9, "免杀 零日交易")
}

func TestNormalizeSecurityPolicyMode(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", SecurityPolicyModeBlockSession},
		{"block_session", SecurityPolicyModeBlockSession},
		{"block_request", SecurityPolicyModeBlockRequest},
		{" BLOCK_REQUEST ", SecurityPolicyModeBlockRequest},
		{"nonsense", SecurityPolicyModeBlockSession},
	}
	for _, tc := range cases {
		if got := NormalizeSecurityPolicyMode(tc.input); got != tc.want {
			t.Fatalf("NormalizeSecurityPolicyMode(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func securityPolicyInt64Ptr(value int64) *int64 { return &value }

// --- EvaluateRequest 测试替身 ---

type securityPolicyTestKeywordRepo struct {
	words []SecurityPolicyKeyword
	err   error
}

func (r *securityPolicyTestKeywordRepo) ListEffectiveKeywords(context.Context, *int64) ([]SecurityPolicyKeyword, error) {
	return r.words, r.err
}

func (r *securityPolicyTestKeywordRepo) ListKeywords(context.Context, *int64, bool) ([]SecurityPolicyKeyword, error) {
	return r.words, r.err
}

func (r *securityPolicyTestKeywordRepo) CreateKeyword(_ context.Context, in SecurityPolicyKeywordInput) (*SecurityPolicyKeyword, error) {
	return &SecurityPolicyKeyword{ID: 1, Keyword: in.Keyword, Category: in.Category, Enabled: true}, nil
}

func (r *securityPolicyTestKeywordRepo) UpdateKeyword(_ context.Context, id int64, in SecurityPolicyKeywordUpdate) (*SecurityPolicyKeyword, error) {
	return &SecurityPolicyKeyword{ID: id, Enabled: true}, nil
}

func (r *securityPolicyTestKeywordRepo) DeleteKeyword(context.Context, int64) error { return nil }

type securityPolicyTestSessionStore struct {
	blocked map[string]map[string]bool
	findErr error
}

func (s *securityPolicyTestSessionStore) MarkSessionBlocked(_ context.Context, scope string, fields []string, _ time.Duration) error {
	if s.blocked == nil {
		s.blocked = make(map[string]map[string]bool)
	}
	if s.blocked[scope] == nil {
		s.blocked[scope] = make(map[string]bool)
	}
	for _, field := range fields {
		s.blocked[scope][field] = true
	}
	return nil
}

func (s *securityPolicyTestSessionStore) FindBlockedSessionField(_ context.Context, scope string, fields []string) (string, error) {
	if s.findErr != nil {
		return "", s.findErr
	}
	for _, field := range fields {
		if s.blocked[scope][field] {
			return field, nil
		}
	}
	return "", nil
}

func (s *securityPolicyTestSessionStore) UnblockSessionScope(_ context.Context, scope string) (bool, error) {
	if len(s.blocked[scope]) == 0 {
		return false, nil
	}
	delete(s.blocked, scope)
	return true, nil
}

type securityPolicyTestModerationRepo struct {
	logs       []ContentModerationLog
	overturned []int64
}

func (r *securityPolicyTestModerationRepo) CreateLog(_ context.Context, log *ContentModerationLog) error {
	log.ID = int64(len(r.logs) + 1)
	r.logs = append(r.logs, *log)
	return nil
}

func (r *securityPolicyTestModerationRepo) ListLogs(context.Context, ContentModerationLogFilter) ([]ContentModerationLog, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

func (r *securityPolicyTestModerationRepo) CountFlaggedByUserSince(context.Context, int64, time.Time, bool) (int, error) {
	return 0, nil
}

func (r *securityPolicyTestModerationRepo) CleanupExpiredLogs(context.Context, time.Time, time.Time) (*ContentModerationCleanupResult, error) {
	return &ContentModerationCleanupResult{}, nil
}

func (r *securityPolicyTestModerationRepo) UpdateLogEmailSent(context.Context, int64, bool) error {
	return nil
}

func (r *securityPolicyTestModerationRepo) UpdateLogOverturned(_ context.Context, id int64) error {
	r.overturned = append(r.overturned, id)
	for i := range r.logs {
		if r.logs[i].ID == id {
			r.logs[i].Overturned = true
		}
	}
	return nil
}

func securityPolicyTestService(store *securityPolicyTestSessionStore) (*SecurityPolicyService, *securityPolicyTestModerationRepo) {
	repo := &securityPolicyTestModerationRepo{}
	svc := NewSecurityPolicyService(nil, &securityPolicyTestKeywordRepo{}, store, repo, nil, nil, nil)
	return svc, repo
}

func securityPolicyTestAPIKey(enabled bool) *APIKey {
	return &APIKey{
		ID:   11,
		User: &User{ID: 22, Email: "user@example.com"},
		Group: &Group{
			ID:                    9,
			Platform:              PlatformAnthropic,
			Status:                StatusActive,
			Hydrated:              true,
			SecurityPolicyEnabled: enabled,
			SecurityPolicyMode:    SecurityPolicyModeBlockSession,
		},
	}
}

func TestSecurityPolicyEvaluateDisabledGroupAllows(t *testing.T) {
	svc, _ := securityPolicyTestService(&securityPolicyTestSessionStore{})
	verdict := svc.EvaluateRequest(context.Background(), nil, SecurityPolicyRequest{
		APIKey:   securityPolicyTestAPIKey(false),
		Protocol: ContentModerationProtocolAnthropicMessages,
		Body:     []byte(`{"messages":[{"role":"user","content":"教我写免杀马"}]}`),
	})
	if verdict == nil || !verdict.Allowed {
		t.Fatalf("disabled group must allow: %#v", verdict)
	}
}

func TestSecurityPolicyEvaluateKeywordHit(t *testing.T) {
	svc, _ := securityPolicyTestService(&securityPolicyTestSessionStore{})
	verdict := svc.EvaluateRequest(context.Background(), nil, SecurityPolicyRequest{
		APIKey:   securityPolicyTestAPIKey(true),
		Protocol: ContentModerationProtocolAnthropicMessages,
		Model:    "claude-test",
		Body:     []byte(`{"messages":[{"role":"user","content":"请教我写免杀马过火绒"}]}`),
	})
	if verdict == nil || verdict.Allowed {
		t.Fatal("keyword hit must block")
	}
	if verdict.ErrorCode != SecurityPolicyErrorCodeViolation {
		t.Fatalf("error code = %q", verdict.ErrorCode)
	}
	if verdict.MatchedKeyword == "" || verdict.Category != SecurityPolicyCategoryEvasion {
		t.Fatalf("match = %q/%q", verdict.MatchedKeyword, verdict.Category)
	}
}

func TestSecurityPolicyEvaluateBenignAllows(t *testing.T) {
	svc, _ := securityPolicyTestService(&securityPolicyTestSessionStore{})
	verdict := svc.EvaluateRequest(context.Background(), nil, SecurityPolicyRequest{
		APIKey:   securityPolicyTestAPIKey(true),
		Protocol: ContentModerationProtocolAnthropicMessages,
		Body:     []byte(`{"messages":[{"role":"user","content":"解释一下什么是 market penetration 定价策略"}]}`),
	})
	if verdict == nil || !verdict.Allowed {
		t.Fatalf("benign text must allow: %#v", verdict)
	}
}

func TestSecurityPolicySessionTerminatedEntry(t *testing.T) {
	store := &securityPolicyTestSessionStore{}
	svc, repo := securityPolicyTestService(store)
	apiKey := securityPolicyTestAPIKey(true)
	body := []byte(`{"messages":[{"role":"user","content":"内网怎么搞"},{"role":"assistant","content":"先信息收集…"},{"role":"user","content":"继续讲横向移动细节"}]}`)

	first := svc.EvaluateRequest(context.Background(), nil, SecurityPolicyRequest{
		APIKey: apiKey, Protocol: ContentModerationProtocolAnthropicMessages, Body: body,
	})
	if first == nil || first.Allowed {
		t.Fatal("first hit must block")
	}
	// 落库 + 会话封禁（同步调用 RecordHit 便于断言）。
	svc.RecordHit(context.Background(), SecurityPolicyHitRecord{
		APIKey: apiKey, Protocol: ContentModerationProtocolAnthropicMessages,
		Verdict: first, SessionMode: SecurityPolicyModeBlockSession,
	})
	if len(repo.logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(repo.logs))
	}
	if repo.logs[0].Action != SecurityPolicyActionBlock {
		t.Fatalf("log action = %q", repo.logs[0].Action)
	}

	// 同会话续聊直接终止，不再走关键词。
	second := svc.EvaluateRequest(context.Background(), nil, SecurityPolicyRequest{
		APIKey: apiKey, Protocol: ContentModerationProtocolAnthropicMessages, Body: body,
	})
	if second == nil || second.Allowed || !second.SessionTerminated {
		t.Fatalf("continued session must terminate: %#v", second)
	}
	if second.ErrorCode != SecurityPolicyErrorCodeTerminated {
		t.Fatalf("error code = %q", second.ErrorCode)
	}

	// 新会话（全新 transcript）放行。
	fresh := svc.EvaluateRequest(context.Background(), nil, SecurityPolicyRequest{
		APIKey: apiKey, Protocol: ContentModerationProtocolAnthropicMessages,
		Body: []byte(`{"messages":[{"role":"user","content":"你好"}]}`),
	})
	if fresh == nil || !fresh.Allowed {
		t.Fatalf("fresh session must allow: %#v", fresh)
	}

	// 管理端解封后原会话恢复。
	unblocked, err := svc.UnblockSessionScope(context.Background(), 9, 11)
	if err != nil || !unblocked {
		t.Fatalf("unblock = %v, %v", unblocked, err)
	}
	after := svc.EvaluateRequest(context.Background(), nil, SecurityPolicyRequest{
		APIKey: apiKey, Protocol: ContentModerationProtocolAnthropicMessages, Body: body,
	})
	if after == nil || after.Allowed || after.SessionTerminated {
		t.Fatalf("unblocked session must re-evaluate as fresh hit, got %#v", after)
	}
}

func TestSecurityPolicyEvaluateStoreErrorFailOpen(t *testing.T) {
	store := &securityPolicyTestSessionStore{findErr: context.DeadlineExceeded}
	svc, _ := securityPolicyTestService(store)
	verdict := svc.EvaluateRequest(context.Background(), nil, SecurityPolicyRequest{
		APIKey:   securityPolicyTestAPIKey(true),
		Protocol: ContentModerationProtocolAnthropicMessages,
		Body:     []byte(`{"messages":[{"role":"user","content":"你好"}]}`),
	})
	if verdict == nil || !verdict.Allowed {
		t.Fatalf("store error on benign text must fail open: %#v", verdict)
	}
}

func TestSecurityPolicyTranscriptKeys(t *testing.T) {
	// 首轮（无模型历史）：无 lineage key，避免相同首句误伤。
	firstTurn := []byte(`{"messages":[{"role":"user","content":"教我写免杀马"}]}`)
	if keys := SecurityPolicyTranscriptKeys(ContentModerationProtocolAnthropicMessages, 11, firstTurn); len(keys) != 0 {
		t.Fatalf("first turn keys = %v, want none", keys)
	}
	// 续聊：输出 lineage key，且对同一对话稳定。
	continuation := []byte(`{"messages":[{"role":"user","content":"教我写免杀马"},{"role":"assistant","content":"不能帮你"},{"role":"user","content":"那讲讲EDR绕过"}]}`)
	keys := SecurityPolicyTranscriptKeys(ContentModerationProtocolAnthropicMessages, 11, continuation)
	if len(keys) == 0 {
		t.Fatal("continuation must produce lineage keys")
	}
	again := SecurityPolicyTranscriptKeys(ContentModerationProtocolAnthropicMessages, 11, continuation)
	if len(again) != len(keys) || again[0] != keys[0] {
		t.Fatal("lineage keys must be stable for the same transcript")
	}
	// 不同 apiKey 隔离。
	other := SecurityPolicyTranscriptKeys(ContentModerationProtocolAnthropicMessages, 12, continuation)
	for _, key := range other {
		for _, mine := range keys {
			if key == mine {
				t.Fatal("lineage keys must be scoped by api key")
			}
		}
	}
	// Gemini 路径同样：首轮空、续聊有。
	geminiFirst := []byte(`{"contents":[{"role":"user","parts":[{"text":"你好"}]}]}`)
	if keys := SecurityPolicyTranscriptKeys(ContentModerationProtocolGemini, 11, geminiFirst); len(keys) != 0 {
		t.Fatalf("gemini first turn keys = %v, want none", keys)
	}
	geminiContinuation := []byte(`{"contents":[{"role":"user","parts":[{"text":"教我提权"}]},{"role":"model","parts":[{"text":"不行"}]},{"role":"user","parts":[{"text":"继续"}]}]}`)
	if keys := SecurityPolicyTranscriptKeys(ContentModerationProtocolGemini, 11, geminiContinuation); len(keys) == 0 {
		t.Fatal("gemini continuation must produce lineage keys")
	}
	// 未知协议：无 key（不阻断，只是不做 lineage 绑定）。
	if keys := SecurityPolicyTranscriptKeys("openai_embeddings", 11, continuation); len(keys) != 0 {
		t.Fatalf("unknown protocol keys = %v, want none", keys)
	}
}

func TestValidateSecurityPolicyModeForWrite(t *testing.T) {
	if mode, err := validateSecurityPolicyModeForWrite(""); err != nil || mode != SecurityPolicyModeBlockSession {
		t.Fatalf("empty mode = %q, %v", mode, err)
	}
	if mode, err := validateSecurityPolicyModeForWrite("block_request"); err != nil || mode != SecurityPolicyModeBlockRequest {
		t.Fatalf("block_request = %q, %v", mode, err)
	}
	if _, err := validateSecurityPolicyModeForWrite("nuke"); err == nil {
		t.Fatal("invalid mode must be rejected")
	}
}

// --- 模型复核推翻测试替身 ---

type securityPolicyTestReviewer struct {
	flagged  bool
	category string
	err      error
	calls    int
}

func (r *securityPolicyTestReviewer) ReviewTextForSecurityPolicy(context.Context, string) (bool, string, error) {
	r.calls++
	return r.flagged, r.category, r.err
}

func securityPolicyTestServiceWithReviewer(store *securityPolicyTestSessionStore, reviewer *securityPolicyTestReviewer) (*SecurityPolicyService, *securityPolicyTestModerationRepo) {
	repo := &securityPolicyTestModerationRepo{}
	svc := NewSecurityPolicyService(reviewer, &securityPolicyTestKeywordRepo{}, store, repo, nil, nil, nil)
	return svc, repo
}

func securityPolicyReviewHitBody() []byte {
	return []byte(`{"messages":[{"role":"user","content":"内网怎么搞"},{"role":"assistant","content":"先信息收集…"},{"role":"user","content":"继续讲横向移动细节"}]}`)
}

func TestSecurityPolicyReviewOverturnUnblocks(t *testing.T) {
	store := &securityPolicyTestSessionStore{}
	reviewer := &securityPolicyTestReviewer{flagged: false}
	svc, repo := securityPolicyTestServiceWithReviewer(store, reviewer)
	apiKey := securityPolicyTestAPIKey(true)

	verdict := svc.EvaluateRequest(context.Background(), nil, SecurityPolicyRequest{
		APIKey: apiKey, Protocol: ContentModerationProtocolAnthropicMessages, Body: securityPolicyReviewHitBody(),
	})
	if verdict == nil || verdict.Allowed {
		t.Fatal("keyword hit must block first")
	}
	svc.RecordHit(context.Background(), SecurityPolicyHitRecord{
		APIKey: apiKey, Protocol: ContentModerationProtocolAnthropicMessages,
		Verdict: verdict, SessionMode: SecurityPolicyModeBlockSession,
	})
	if reviewer.calls != 1 {
		t.Fatalf("reviewer calls = %d, want 1", reviewer.calls)
	}
	if len(repo.overturned) != 1 {
		t.Fatalf("overturned logs = %v", repo.overturned)
	}
	// 会话已解封：原请求再次评估回到新鲜命中（拦截但非终止）。
	after := svc.EvaluateRequest(context.Background(), nil, SecurityPolicyRequest{
		APIKey: apiKey, Protocol: ContentModerationProtocolAnthropicMessages, Body: securityPolicyReviewHitBody(),
	})
	if after == nil || after.Allowed || after.SessionTerminated {
		t.Fatalf("overturned session must re-evaluate as fresh hit, got %#v", after)
	}
}

func TestSecurityPolicyReviewConfirmKeepsBlock(t *testing.T) {
	store := &securityPolicyTestSessionStore{}
	reviewer := &securityPolicyTestReviewer{flagged: true, category: "illicit/violent"}
	svc, repo := securityPolicyTestServiceWithReviewer(store, reviewer)
	apiKey := securityPolicyTestAPIKey(true)

	verdict := svc.EvaluateRequest(context.Background(), nil, SecurityPolicyRequest{
		APIKey: apiKey, Protocol: ContentModerationProtocolAnthropicMessages, Body: securityPolicyReviewHitBody(),
	})
	svc.RecordHit(context.Background(), SecurityPolicyHitRecord{
		APIKey: apiKey, Protocol: ContentModerationProtocolAnthropicMessages,
		Verdict: verdict, SessionMode: SecurityPolicyModeBlockSession,
	})
	if len(repo.overturned) != 0 {
		t.Fatalf("confirmed hit must not overturn: %v", repo.overturned)
	}
	second := svc.EvaluateRequest(context.Background(), nil, SecurityPolicyRequest{
		APIKey: apiKey, Protocol: ContentModerationProtocolAnthropicMessages, Body: securityPolicyReviewHitBody(),
	})
	if second == nil || second.Allowed || !second.SessionTerminated {
		t.Fatalf("confirmed session must stay terminated: %#v", second)
	}
}

func TestSecurityPolicyReviewSkippedForBlockRequestMode(t *testing.T) {
	store := &securityPolicyTestSessionStore{}
	reviewer := &securityPolicyTestReviewer{flagged: false}
	svc, _ := securityPolicyTestServiceWithReviewer(store, reviewer)
	apiKey := securityPolicyTestAPIKey(true)

	verdict := svc.EvaluateRequest(context.Background(), nil, SecurityPolicyRequest{
		APIKey: apiKey, Protocol: ContentModerationProtocolAnthropicMessages, Body: securityPolicyReviewHitBody(),
	})
	svc.RecordHit(context.Background(), SecurityPolicyHitRecord{
		APIKey: apiKey, Protocol: ContentModerationProtocolAnthropicMessages,
		Verdict: verdict, SessionMode: SecurityPolicyModeBlockRequest,
	})
	if reviewer.calls != 0 {
		t.Fatalf("block_request mode must not trigger review, calls = %d", reviewer.calls)
	}
}

func TestSecurityPolicyReviewErrorKeepsBlock(t *testing.T) {
	store := &securityPolicyTestSessionStore{}
	reviewer := &securityPolicyTestReviewer{err: context.DeadlineExceeded}
	svc, repo := securityPolicyTestServiceWithReviewer(store, reviewer)
	apiKey := securityPolicyTestAPIKey(true)

	verdict := svc.EvaluateRequest(context.Background(), nil, SecurityPolicyRequest{
		APIKey: apiKey, Protocol: ContentModerationProtocolAnthropicMessages, Body: securityPolicyReviewHitBody(),
	})
	svc.RecordHit(context.Background(), SecurityPolicyHitRecord{
		APIKey: apiKey, Protocol: ContentModerationProtocolAnthropicMessages,
		Verdict: verdict, SessionMode: SecurityPolicyModeBlockSession,
	})
	if len(repo.overturned) != 0 {
		t.Fatalf("review error must keep block: %v", repo.overturned)
	}
}

func TestSecurityPolicyViolationEmailCooldown(t *testing.T) {
	store := &securityPolicyTestSessionStore{}
	svc, _ := securityPolicyTestService(store)

	// 首封放行，窗内重复命中节流，不同收件人互不影响。
	if !svc.allowViolationEmail("User@Example.com", 11) {
		t.Fatal("first email must be allowed")
	}
	if svc.allowViolationEmail("user@example.com", 11) {
		t.Fatal("repeat hit inside cooldown must be throttled")
	}
	if !svc.allowViolationEmail("other@example.com", 11) {
		t.Fatal("different recipient must not be throttled")
	}
	// 无邮箱时退化到 apiKeyID 隔离。
	if !svc.allowViolationEmail("", 12) {
		t.Fatal("empty email with fresh key id must be allowed")
	}
	if svc.allowViolationEmail("   ", 12) {
		t.Fatal("same key id inside cooldown must be throttled")
	}
	// 过窗恢复。
	svc.emailThrottleMu.Lock()
	svc.emailThrottle["mail:user@example.com"] = time.Now().Add(-2 * securityPolicyViolationEmailCooldown)
	svc.emailThrottleMu.Unlock()
	if !svc.allowViolationEmail("user@example.com", 11) {
		t.Fatal("email must be allowed again after cooldown")
	}
}
