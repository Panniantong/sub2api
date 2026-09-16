package handler

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

type capabilityRepo struct {
	service.IntelligentTestRepository
	visible bool
	user    int64
}

func (r *capabilityRepo) PublicGet(_ context.Context, user, id int64) (*service.PublicAccountTest, error) {
	r.user = user
	if !r.visible {
		return nil, service.ErrIntelligentTestNotFound
	}
	return &service.PublicAccountTest{ID: id, Result: "visible result"}, nil
}
func TestIntelligentPublicHandlerUsesCurrentVisibilityAndIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &capabilityRepo{}
	h := NewAccountCapabilityHandler(service.NewIntelligentTestService(repo, nil))
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 27}) })
	engine.GET("/results/:id", h.Get)
	engine.GET("/results/:id/image", h.Image)
	for _, path := range []string{"/results/1", "/results/1/image"} {
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, 404, w.Code)
		require.EqualValues(t, 27, repo.user)
	}
	repo.visible = true
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/results/1", nil))
	require.Equal(t, 200, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	data := body["data"].(map[string]any)
	for _, key := range []string{"credentials", "notes", "input", "raw_response", "error_message", "config_snapshot", "evaluation"} {
		_, exists := data[key]
		require.False(t, exists, key)
	}
	repo.visible = false
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/results/1", nil))
	require.Equal(t, 404, w.Code)
}
