package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type intelligentHandlerRepo struct {
	service.IntelligentTestRepository
	admin  bool
	record *service.IntelligentTestRecord
	calls  int
}

func (r *intelligentHandlerRepo) IsAdmin(context.Context, int64) (bool, error) { return r.admin, nil }
func (r *intelligentHandlerRepo) Get(context.Context, int64) (*service.IntelligentTestRecord, error) {
	r.calls++
	return r.record, nil
}
func (r *intelligentHandlerRepo) Accounts(context.Context, service.IntelligentTestFilter) (*service.IntelligentTestAccounts, error) {
	r.calls++
	return &service.IntelligentTestAccounts{}, nil
}
func intelligentHandlerEngine(repo *intelligentHandlerRepo, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	h := NewIntelligentTestHandler(service.NewIntelligentTestService(repo, nil))
	engine.Use(func(c *gin.Context) {
		if role != "" {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
			c.Set(string(middleware.ContextKeyUserRole), role)
		}
	})
	engine.GET("/accounts", h.Accounts)
	engine.GET("/records/:id", h.Get)
	engine.GET("/records/:id/image", h.Image)
	engine.POST("/run", h.Run)
	engine.POST("/evaluate-preview", h.PreviewEvaluation)
	engine.POST("/records/:id/cancel", h.Cancel)
	engine.POST("/records/:id/reevaluate", h.Reevaluate)
	return engine
}
func TestIntelligentHandlerAdminAuthorization(t *testing.T) {
	for _, tc := range []struct {
		role    string
		dbAdmin bool
		want    int
	}{{"", false, 401}, {"user", false, 403}, {"admin", false, 403}, {"admin", true, 200}, {"super_admin", true, 200}} {
		t.Run(tc.role, func(t *testing.T) {
			repo := &intelligentHandlerRepo{admin: tc.dbAdmin}
			engine := intelligentHandlerEngine(repo, tc.role)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/accounts", nil))
			require.Equal(t, tc.want, w.Code)
			if tc.want != 200 {
				require.Zero(t, repo.calls)
			}
		})
	}
}
func TestIntelligentHandlerRejectsMalformedInputs(t *testing.T) {
	repo := &intelligentHandlerRepo{admin: true}
	engine := intelligentHandlerEngine(repo, "admin")
	for _, path := range []string{"/records/-1", "/accounts?anti_degradation=maybe", "/accounts?from=tomorrow", "/accounts?page=0", "/accounts?group_id=-1"} {
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, 400, w.Code, path)
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/run", strings.NewReader(`{"account_ids":[1,1],"test_types":["candy"],"idempotency_key":"repeat-operation-key"}`)))
	require.Equal(t, 400, w.Code)
	require.Zero(t, repo.calls)
}
func TestIntelligentHandlerImageSandboxAndValidation(t *testing.T) {
	repo := &intelligentHandlerRepo{admin: true, record: &service.IntelligentTestRecord{ResultImage: `<svg viewBox="0 0 10 10"><circle r="2"/></svg>`}}
	engine := intelligentHandlerEngine(repo, "admin")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/records/1/image", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Header().Get("Content-Type"), "image/svg+xml")
	require.Contains(t, w.Header().Get("Content-Security-Policy"), "sandbox")
	require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
	repo.record.ResultImage = `<svg onload="alert(1)"><circle r="2"/></svg>`
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/records/1/image", nil))
	require.Equal(t, 404, w.Code)
	require.NotContains(t, w.Body.String(), "onload")
}

func TestIntelligentHandlerHistoricalImageAndNewActionsAuthorization(t *testing.T) {
	repo := &intelligentHandlerRepo{admin: true, record: &service.IntelligentTestRecord{Result: `<svg viewBox="0 0 100 100"><style>.bird{fill:orange}</style><circle class="bird" cx="50" cy="50" r="20"/><script>alert(1)</script></svg>`}}
	engine := intelligentHandlerEngine(repo, "admin")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/records/1/image", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `fill="orange"`)
	require.NotContains(t, w.Body.String(), "script")
	require.NotContains(t, w.Body.String(), "<style")
	require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	for _, role := range []string{"", "user", "admin"} {
		forbidden := &intelligentHandlerRepo{admin: false}
		restricted := intelligentHandlerEngine(forbidden, role)
		for _, path := range []string{"/evaluate-preview", "/records/1/cancel", "/records/1/reevaluate"} {
			w := httptest.NewRecorder()
			restricted.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
			require.Contains(t, []int{401, 403}, w.Code)
			require.Zero(t, forbidden.calls)
		}
	}
}

func TestIntelligentHandlerDraftPreviewSeparatesVerdictsWithoutRunner(t *testing.T) {
	repo := &intelligentHandlerRepo{admin: true}
	engine := intelligentHandlerEngine(repo, "admin") // nil runner: local rule only
	body, err := json.Marshal(map[string]any{"output": "**ANSWER: 12颗**", "config": service.IntelligentTestConfig{Prompt: strings.Repeat("中", 6000), Evaluator: "exact_answer", ExpectedAnswer: "12", AnswerType: "number", AnswerUnit: "颗", TimeoutSeconds: 120}})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/evaluate-preview", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"answer_verdict":"correct"`)
	require.Contains(t, w.Body.String(), `"format_verdict":"non_compliant"`)
	require.Contains(t, w.Body.String(), `"status":"completed"`)
	require.Zero(t, repo.calls)
}
