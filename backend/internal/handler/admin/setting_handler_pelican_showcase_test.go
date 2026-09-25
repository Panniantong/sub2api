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

func getShowcaseSettings(t *testing.T, h *SettingHandler) (bool, service.PelicanShowcaseConfig) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
	h.GetSettings(c)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var envelope struct {
		Data struct {
			Enabled bool                          `json:"pelican_showcase_enabled"`
			Config  service.PelicanShowcaseConfig `json:"pelican_showcase_config"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	return envelope.Data.Enabled, envelope.Data.Config
}

func TestSettingsPelicanShowcaseDefaultsRoundTripAndOmission(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	enabled, cfg := getShowcaseSettings(t, h)
	require.False(t, enabled, "the gallery is opt-in")
	require.Equal(t, service.DefaultPelicanShowcaseConfig(), cfg)

	rec := doUpdateSettings(t, h, map[string]any{
		"pelican_showcase_enabled": true,
		"pelican_showcase_config":  map[string]any{"group_ids": []int64{9, 3, 9}, "max_items": 30, "auto_cleanup": false, "retention_days": 14},
	}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "true", repo.values[service.SettingKeyPelicanShowcaseEnabled])
	require.JSONEq(t, `{"group_ids":[3,9],"max_items":30,"auto_cleanup":false,"retention_days":14}`,
		repo.values[service.SettingKeyPelicanShowcaseConfig])
	enabled, cfg = getShowcaseSettings(t, h)
	require.True(t, enabled)
	require.Equal(t, service.PelicanShowcaseConfig{GroupIDs: []int64{3, 9}, MaxItems: 30, RetentionDays: 14}, cfg)

	runtime, err := h.settingService.GetPelicanShowcaseRuntime(context.Background())
	require.NoError(t, err)
	require.True(t, runtime.Enabled)
	require.Equal(t, cfg, runtime.Config)
	public, err := h.settingService.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.True(t, public.PelicanShowcaseEnabled, "the sidebar flag is public")

	saved := repo.values[service.SettingKeyPelicanShowcaseConfig]
	rec = doUpdateSettings(t, h, map[string]any{"site_name": "unrelated"}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "true", repo.values[service.SettingKeyPelicanShowcaseEnabled])
	require.Equal(t, saved, repo.values[service.SettingKeyPelicanShowcaseConfig])
}

func TestSettingsPelicanShowcaseRejectsOutOfRangeLimits(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	rec := doUpdateSettings(t, h, map[string]any{
		"pelican_showcase_config": map[string]any{"group_ids": []int64{2}, "max_items": 5, "auto_cleanup": true, "retention_days": 3},
	}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	saved := repo.values[service.SettingKeyPelicanShowcaseConfig]

	for _, bad := range []map[string]any{
		{"group_ids": []int64{2}, "max_items": 101, "retention_days": 3},
		{"group_ids": []int64{2}, "max_items": 5, "retention_days": 91},
		{"group_ids": []int64{-1}, "max_items": 5, "retention_days": 3},
	} {
		rec = doUpdateSettings(t, h, map[string]any{"pelican_showcase_config": bad}, nil)
		require.Equal(t, http.StatusBadRequest, rec.Code, "%v: %s", bad, rec.Body.String())
		require.Equal(t, saved, repo.values[service.SettingKeyPelicanShowcaseConfig])
	}
}

func TestSettingsPelicanShowcaseValidatesOnlyNewlyAddedGroups(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	reader := &ticketHarvestGroupReader{groups: map[int64]*service.Group{
		2: {ID: 2, Platform: service.PlatformOpenAI},
		3: {ID: 3, Platform: service.PlatformAnthropic},
	}}
	h.settingService.SetDefaultSubscriptionGroupReader(reader)

	rec := doUpdateSettings(t, h, map[string]any{"pelican_showcase_config": map[string]any{"group_ids": []int64{2, 3}}}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	saved := repo.values[service.SettingKeyPelicanShowcaseConfig]

	rec = doUpdateSettings(t, h, map[string]any{"pelican_showcase_config": map[string]any{"group_ids": []int64{2, 3, 999}}}, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "INVALID_PELICAN_SHOWCASE_GROUP")
	require.Equal(t, saved, repo.values[service.SettingKeyPelicanShowcaseConfig])

	// A group deleted after being selected neither blocks unrelated saves nor re-saving the selection.
	delete(reader.groups, 3)
	rec = doUpdateSettings(t, h, map[string]any{"site_name": "keep-showcase"}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = doUpdateSettings(t, h, map[string]any{"pelican_showcase_config": map[string]any{"group_ids": []int64{2, 3}}}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, saved, repo.values[service.SettingKeyPelicanShowcaseConfig])
}
