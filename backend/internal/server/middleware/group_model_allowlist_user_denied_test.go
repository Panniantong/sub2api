package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// deniedModelsAPIKey 构造一个所属用户在分组内禁用了部分模型的 Key；allowlist 为 nil 时分组不开白名单。
func deniedModelsAPIKey(allowlist []string, denied ...string) *service.APIKey {
	return &service.APIKey{
		User: &service.User{ID: 7, UserGroupDeniedModels: denied},
		Group: &service.Group{
			Platform: service.PlatformOpenAI,
			ModelAllowlist: service.GroupModelAllowlist{
				Enabled: allowlist != nil,
				Models:  allowlist,
			},
		},
	}
}

func TestUserGroupDeniedModelRejected(t *testing.T) {
	router, calls := newGroupModelAllowlistTestRouter(deniedModelsAPIKey(nil, "gpt-6-luna"), "/v1")

	w := doJSON(t, router, http.MethodPost, "/v1/responses", `{"model":"gpt-6-luna"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `gpt-6-luna\" is not available for your account in this group`) {
		t.Fatalf("expected user-specific message, got %s", w.Body.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("expected handler not to run, got %v", *calls)
	}
}

func TestUserGroupDeniedModelOtherModelsAllowed(t *testing.T) {
	router, calls := newGroupModelAllowlistTestRouter(deniedModelsAPIKey(nil, "gpt-6-luna"), "/v1")

	w := doJSON(t, router, http.MethodPost, "/v1/responses", `{"model":"gpt-6-sol"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(*calls) != 1 {
		t.Fatalf("expected handler to run once, got %v", *calls)
	}
}

func TestUserGroupDeniedModelWildcardCaseInsensitive(t *testing.T) {
	router, _ := newGroupModelAllowlistTestRouter(deniedModelsAPIKey(nil, "GPT-6-*"), "/v1")

	if w := doJSON(t, router, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-6-luna"}`); w.Code != http.StatusNotFound {
		t.Fatalf("expected wildcard denial, got %d: %s", w.Code, w.Body.String())
	}
	if w := doJSON(t, router, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-5.5"}`); w.Code != http.StatusOK {
		t.Fatalf("expected other model allowed, got %d: %s", w.Code, w.Body.String())
	}
}

// 请求体里重复的 model 键可能被下游按末值绑定，禁用模型必须对全部候选值生效。
func TestUserGroupDeniedModelDuplicateKeysCannotBypass(t *testing.T) {
	router, calls := newGroupModelAllowlistTestRouter(deniedModelsAPIKey(nil, "gpt-6-luna"), "/v1")

	w := doJSON(t, router, http.MethodPost, "/v1/responses", `{"model":"gpt-6-sol","model":"gpt-6-luna"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("expected handler not to run, got %v", *calls)
	}
}

func TestUserGroupDeniedModelGeminiRouteParam(t *testing.T) {
	router, calls := newGroupModelAllowlistTestRouter(deniedModelsAPIKey(nil, "gemini-2.5-pro"), "/v1beta")

	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-pro:generateContent", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("expected handler not to run, got %v", *calls)
	}
}

// 分组白名单拦截优先：提示仍是「分组不可用」，与用户禁用的提示区分开。
func TestUserGroupDeniedModelAllowlistMessageTakesPrecedence(t *testing.T) {
	router, _ := newGroupModelAllowlistTestRouter(deniedModelsAPIKey([]string{"gpt-6-sol"}, "gpt-6-luna"), "/v1")

	w := doJSON(t, router, http.MethodPost, "/v1/responses", `{"model":"gpt-6-luna"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "is not available for this group") {
		t.Fatalf("expected allowlist message, got %s", w.Body.String())
	}
}

func TestUserGroupDeniedModelNoRestrictionDoesNotReadBody(t *testing.T) {
	router, calls := newGroupModelAllowlistTestRouter(deniedModelsAPIKey(nil), "/v1")

	body := &readTrackingBody{Reader: strings.NewReader(`{"model":"gpt-6-luna"}`)}
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK || len(*calls) != 1 {
		t.Fatalf("expected pass-through, got %d %v", w.Code, *calls)
	}
	if body.read {
		t.Fatal("no allowlist and no denied models: middleware must not read the request body")
	}
}
