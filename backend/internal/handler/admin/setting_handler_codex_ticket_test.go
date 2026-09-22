package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSettingsCodexTicketProxyWriteReadAndHotReload(t *testing.T) {
	key := service.SettingKeyOpenAICodexTicketHarvestProxyURL
	oldProxy := "http://user:old-secret@old.example.com:8080"
	newProxy := "socks5h://user:new-secret@new.example.com:1080"
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{key: oldProxy})
	require.Equal(t, oldProxy, h.settingService.GetOpenAICodexTicketHarvestProxyURL(context.Background()))
	rec := doUpdateSettings(t, h, map[string]any{key: newProxy}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, newProxy, repo.values[key])
	require.Equal(t, newProxy, h.settingService.GetOpenAICodexTicketHarvestProxyURL(context.Background()))
	require.NotContains(t, rec.Body.String(), "new-secret")
	require.Contains(t, rec.Body.String(), `"openai_codex_ticket_harvest_proxy_configured":true`)
	// Omission, empty input and the masked GET value all preserve the real secret.
	for _, body := range []map[string]any{{"site_name": "updated"}, {key: ""}, {key: service.MaskProxyURL(newProxy)}} {
		rec = doUpdateSettings(t, h, body, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, newProxy, repo.values[key])
	}
	get := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(get)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
	h.GetSettings(c)
	require.Equal(t, http.StatusOK, get.Code)
	require.NotContains(t, get.Body.String(), "new-secret")
	require.Contains(t, get.Body.String(), "new.example.com")
}

func TestSettingsCodexTicketRejectInvalidProxyWithoutLeakingPassword(t *testing.T) {
	key := service.SettingKeyOpenAICodexTicketHarvestProxyURL
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{key: "http://previous.example.com:8080"})
	rec := doUpdateSettings(t, h, map[string]any{key: "ftp://user:invalid-secret@proxy.example.com:21"}, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "invalid-secret")
	require.Equal(t, "http://previous.example.com:8080", repo.values[key])
}

func TestSettingsCodexTicketModelsPersistOmissionAndEmpty(t *testing.T) {
	key := service.SettingKeyOpenAICodexTicketModels
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{key: `["gpt-6-astra","gpt-5.6-sol"]`})
	ctx := context.Background()
	require.Len(t, h.settingService.GetOpenAICodexTicketModels(ctx, nil), 2)
	for _, models := range [][]string{{"gpt-6-astra"}, {}} {
		rec := doUpdateSettings(t, h, map[string]any{key: models}, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, models, h.settingService.GetOpenAICodexTicketModels(ctx, nil))
		saved := repo.values[key]
		rec = doUpdateSettings(t, h, map[string]any{"site_name": "updated"}, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, saved, repo.values[key])
	}
}

func TestSettingsCodexTicketRestoresStaticProxyAfterKernel(t *testing.T) {
	key := service.SettingKeyOpenAICodexTicketHarvestProxyURL
	original := "http://user:secret@residential.example:8080"
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{key: original})
	rec := doUpdateSettings(t, h, map[string]any{key: "http://127.0.0.1:3101"}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, original, repo.values[service.SettingKeyOpenAICodexTicketStaticProxyURL])
	require.NotContains(t, rec.Body.String(), ":secret@")
	rec = doUpdateSettings(t, h, map[string]any{key: service.MaskProxyURL(original), "openai_codex_ticket_use_saved_static_proxy": true}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, original, repo.values[key])
}

func TestSettingsCodexTicketStrategyRoundTrip(t *testing.T) {
	key := service.SettingKeyOpenAICodexTicketStrategy
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	require.Equal(t, "standby", h.settingService.GetCodexTicketStrategy(context.Background()))
	for _, strategy := range []string{"fixed", "standby"} {
		rec := doUpdateSettings(t, h, map[string]any{key: strategy}, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, strategy, repo.values[key])
		require.Equal(t, strategy, h.settingService.GetCodexTicketStrategy(context.Background()))
		rec = doUpdateSettings(t, h, map[string]any{"site_name": "kept"}, nil)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, strategy, repo.values[key])
	}
	rec := doUpdateSettings(t, h, map[string]any{key: "unknown"}, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "standby", repo.values[key])
}

func TestSettingsCodexTicketStrictPreservesOmittedValue(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	key := service.SettingKeyOpenAICodexTicketStrict
	require.False(t, h.settingService.CodexTicketStrictResponse(context.Background()))
	rec := doUpdateSettings(t, h, map[string]any{key: true}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	rec = doUpdateSettings(t, h, map[string]any{"site_name": "unchanged-policy"}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "true", repo.values[key])
	rec = doUpdateSettings(t, h, map[string]any{key: false}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "false", repo.values[key])
}

func TestSettingsCodexHarvestScopeRoundTripOmissionAndValidation(t *testing.T) {
	key := service.SettingKeyOpenAICodexTicketHarvestScope
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	for _, scope := range []map[string]any{
		{"mode": "selected", "group_ids": []int64{24, 2, 2}},
		{"mode": "selected", "group_ids": []int64{}},
		{"mode": "all", "group_ids": []int64{}},
	} {
		rec := doUpdateSettings(t, h, map[string]any{key: scope}, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), key)
		saved := repo.values[key]
		rec = doUpdateSettings(t, h, map[string]any{"site_name": "unchanged-scope"}, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, saved, repo.values[key])
	}
	for _, scope := range []map[string]any{
		{"mode": "unknown"}, {"mode": "selected", "group_ids": []int64{-1}}, {"group_ids": []int64{2}},
	} {
		saved := repo.values[key]
		rec := doUpdateSettings(t, h, map[string]any{key: scope}, nil)
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		require.Equal(t, saved, repo.values[key])
	}
}

type ticketHarvestGroupReader struct {
	groups map[int64]*service.Group
}

func (r *ticketHarvestGroupReader) GetByID(_ context.Context, id int64) (*service.Group, error) {
	if group := r.groups[id]; group != nil {
		return group, nil
	}
	return nil, service.ErrGroupNotFound
}

func TestSettingsCodexHarvestScopeValidatesGroupsAndPreservesDeletedSelection(t *testing.T) {
	key := service.SettingKeyOpenAICodexTicketHarvestScope
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	reader := &ticketHarvestGroupReader{groups: map[int64]*service.Group{
		2: {ID: 2, Platform: service.PlatformOpenAI},
		3: {ID: 3, Platform: "grok"},
	}}
	h.settingService.SetDefaultSubscriptionGroupReader(reader)
	rec := doUpdateSettings(t, h, map[string]any{key: map[string]any{"mode": "selected", "group_ids": []int64{2, 2}}}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	scope, err := h.settingService.GetCodexTicketHarvestScope(context.Background())
	require.NoError(t, err)
	require.Equal(t, []int64{2}, scope.GroupIDs)
	saved := repo.values[key]
	for _, id := range []int64{3, 999} {
		rec = doUpdateSettings(t, h, map[string]any{key: map[string]any{"mode": "selected", "group_ids": []int64{id}}}, nil)
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		require.Equal(t, saved, repo.values[key])
	}
	delete(reader.groups, 2)
	rec = doUpdateSettings(t, h, map[string]any{"site_name": "preserve-deleted-selection"}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, saved, repo.values[key])
	get := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(get)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
	h.GetSettings(c)
	require.Equal(t, http.StatusOK, get.Code)
	var envelope struct {
		Data struct {
			Scope service.CodexTicketHarvestScope `json:"openai_codex_ticket_harvest_scope"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(get.Body.Bytes(), &envelope))
	require.Equal(t, scope, envelope.Data.Scope)
}

func TestSettingsCodexTicketFailClosedDefaultsOffAndPersists(t *testing.T) {
	key := service.SettingKeyOpenAICodexTicketFailClosed
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	require.False(t, h.settingService.GetOpenAICodexTicketFailClosed(context.Background()))

	rec := doUpdateSettings(t, h, map[string]any{key: true}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "true", repo.values[key])
	require.True(t, h.settingService.GetOpenAICodexTicketFailClosed(context.Background()))
	require.Contains(t, rec.Body.String(), `"openai_codex_ticket_fail_closed":true`)

	rec = doUpdateSettings(t, h, map[string]any{"site_name": "preserve-ticket-policy"}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "true", repo.values[key])

	rec = doUpdateSettings(t, h, map[string]any{key: false}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "false", repo.values[key])
	require.False(t, h.settingService.GetOpenAICodexTicketFailClosed(context.Background()))
}
