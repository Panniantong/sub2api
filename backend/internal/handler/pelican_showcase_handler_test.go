//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type showcaseSettingRepo struct {
	toggleSettingRepo
}

func (r *showcaseSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

type showcaseHandlerRepo struct {
	service.PelicanShowcaseRepository
	groups  []*service.PelicanShowcaseGroup
	items   []*service.PelicanShowcaseItem
	item    *service.PelicanShowcaseItem
	deleted bool
}

func (r *showcaseHandlerRepo) ListGroups(context.Context, []int64) ([]*service.PelicanShowcaseGroup, error) {
	return r.groups, nil
}
func (r *showcaseHandlerRepo) ListItems(context.Context, []int64, int, time.Time) ([]*service.PelicanShowcaseItem, error) {
	return r.items, nil
}
func (r *showcaseHandlerRepo) GetItem(context.Context, int64, []int64, int, time.Time) (*service.PelicanShowcaseItem, error) {
	return r.item, nil
}
func (r *showcaseHandlerRepo) Delete(context.Context, int64) (bool, error) { return r.deleted, nil }

func newShowcaseHandler(values map[string]string, repo *showcaseHandlerRepo) *PelicanShowcaseHandler {
	settings := service.NewSettingService(&showcaseSettingRepo{toggleSettingRepo{values: values}}, &config.Config{})
	return NewPelicanShowcaseHandler(service.NewPelicanShowcaseService(repo, settings))
}

func serveShowcase(method, path string, handle gin.HandlerFunc) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Handle(method, path, handle)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path[:len(path)-len(":id")]+"7", nil))
	return w
}

func TestPelicanShowcaseHandler_DisabledGalleryIsEmpty(t *testing.T) {
	repo := &showcaseHandlerRepo{groups: []*service.PelicanShowcaseGroup{{ID: 1, Name: "hidden"}}}
	h := newShowcaseHandler(map[string]string{service.SettingKeyPelicanShowcaseConfig: `{"group_ids":[1]}`}, repo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/pelican-showcase", nil)
	h.List(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"code":0,"message":"success","data":{"enabled":false,"max_items":20,"retention_days":0,"groups":[]}}`, w.Body.String())

	require.Equal(t, http.StatusNotFound, serveShowcase(http.MethodGet, "/items/:id", h.GetItem).Code)
}

func TestPelicanShowcaseHandler_ListExposesNoAccountIdentity(t *testing.T) {
	generated := time.Date(2026, 9, 24, 8, 30, 0, 0, time.UTC)
	repo := &showcaseHandlerRepo{
		groups: []*service.PelicanShowcaseGroup{{ID: 3, Name: "VIP", Platform: "openai"}},
		items:  []*service.PelicanShowcaseItem{{ID: 7, GroupID: 3, ModelID: "gpt-6-astra", ReasoningEffort: "high", LatencyMs: 4200, GeneratedAt: generated}},
	}
	h := newShowcaseHandler(map[string]string{
		service.SettingKeyPelicanShowcaseEnabled: "true",
		service.SettingKeyPelicanShowcaseConfig:  `{"group_ids":[3],"max_items":10,"auto_cleanup":true,"retention_days":5}`,
	}, repo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/pelican-showcase", nil)
	h.List(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"code":0,"message":"success","data":{"enabled":true,"max_items":10,"retention_days":5,"groups":[
		{"id":3,"name":"VIP","platform":"openai","items":[
			{"id":7,"group_id":3,"model_id":"gpt-6-astra","reasoning_effort":"high","latency_ms":4200,"generated_at":"2026-09-24T08:30:00Z"}
		]}]}}`, w.Body.String())
}

func TestPelicanShowcaseHandler_ItemAndAdminDelete(t *testing.T) {
	repo := &showcaseHandlerRepo{}
	h := newShowcaseHandler(map[string]string{
		service.SettingKeyPelicanShowcaseEnabled: "true",
		service.SettingKeyPelicanShowcaseConfig:  `{"group_ids":[3]}`,
	}, repo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "abc"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/pelican-showcase/items/abc", nil)
	h.GetItem(c)
	require.Equal(t, http.StatusBadRequest, w.Code)

	require.Equal(t, http.StatusNotFound, serveShowcase(http.MethodGet, "/items/:id", h.GetItem).Code,
		"a snapshot outside the gallery limits is not served")
	repo.item = &service.PelicanShowcaseItem{ID: 7, GroupID: 3, ResponseText: "<svg></svg>"}
	w = serveShowcase(http.MethodGet, "/items/:id", h.GetItem)
	require.Equal(t, http.StatusOK, w.Code)
	var envelope struct {
		Data service.PelicanShowcaseItem `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.Equal(t, "<svg></svg>", envelope.Data.ResponseText)

	require.Equal(t, http.StatusNotFound, serveShowcase(http.MethodDelete, "/items/:id", h.DeleteItem).Code)
	repo.deleted = true
	require.Equal(t, http.StatusOK, serveShowcase(http.MethodDelete, "/items/:id", h.DeleteItem).Code)
}
