package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type harvestAdminSettings struct {
	service.SettingRepository
	raw  string
	fail bool
}

func (s *harvestAdminSettings) GetValue(context.Context, string) (string, error) { return s.raw, nil }
func (s *harvestAdminSettings) Set(_ context.Context, _, raw string) error {
	if s.fail {
		return errors.New("fixture failure")
	}
	s.raw = raw
	return nil
}

type harvestAdminNodes struct {
	service.CodexHarvestNodeRepository
	resetIDs []int64
	fail     bool
}

func (n *harvestAdminNodes) Reset(_ context.Context, id int64) error {
	if n.fail {
		return errors.New("fixture failure")
	}
	n.resetIDs = append(n.resetIDs, id)
	return nil
}
func (n *harvestAdminNodes) List(context.Context, int, int) (service.CodexHarvestNodePage, error) {
	if n.fail {
		return service.CodexHarvestNodePage{}, errors.New("fixture failure")
	}
	return service.CodexHarvestNodePage{Items: []service.CodexHarvestNodeRecord{}}, nil
}
func harvestAdminRouter(h *AccountHandler) *gin.Engine {
	r := gin.New()
	r.GET("/controls", h.GetCodexHarvestControls)
	r.PUT("/controls", h.UpdateCodexHarvestControls)
	r.GET("/nodes", h.GetCodexHarvestNodes)
	r.POST("/reset", h.ResetCodexHarvestNodes)
	return r
}
func harvestAdminRequest(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}
func TestHarvestControlsAdminValidationAndPersistence(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	settings := &harvestAdminSettings{}
	h := &AccountHandler{codexHarvest: service.NewCodexHarvestService(&harvestAdminNodes{}, settings, nil)}
	r := harvestAdminRouter(h)
	require.Equal(t, 200, harvestAdminRequest(r, "GET", "/controls", "").Code)
	v := service.CodexHarvestControls{Version: 1, Speed: service.CodexHarvestSpeedPresets()["standard"]}
	v.Speed.MaxNodeAttempts = 11
	body, err := json.Marshal(v)
	require.NoError(t, err)
	require.Equal(t, 400, harvestAdminRequest(r, "PUT", "/controls", string(body)).Code)
	require.Empty(t, settings.raw)
	v.Speed.MaxNodeAttempts = 3
	v.NodeMemoryEnabled = true
	body, err = json.Marshal(v)
	require.NoError(t, err)
	require.Equal(t, 200, harvestAdminRequest(r, "PUT", "/controls", string(body)).Code)
	var stored service.CodexHarvestControls
	require.NoError(t, json.Unmarshal([]byte(settings.raw), &stored))
	require.Equal(t, v, stored)
	settings.fail = true
	require.Equal(t, 503, harvestAdminRequest(r, "PUT", "/controls", string(body)).Code)
}
func TestHarvestNodesAdminResetIsExplicitAndIsolated(t *testing.T) {
	nodes := &harvestAdminNodes{}
	r := harvestAdminRouter(&AccountHandler{codexHarvest: service.NewCodexHarvestService(nodes, &harvestAdminSettings{}, nil)})
	for _, body := range []string{`{}`, `{"record_id":null}`, `{"record_id":-1}`, `{"record_id":"all"}`} {
		require.Equal(t, 400, harvestAdminRequest(r, "POST", "/reset", body).Code)
	}
	require.Empty(t, nodes.resetIDs)
	for _, body := range []string{`{"record_id":7}`, `{"record_id":0}`} {
		require.Equal(t, 200, harvestAdminRequest(r, "POST", "/reset", body).Code)
	}
	require.Equal(t, []int64{7, 0}, nodes.resetIDs)
	for _, query := range []string{"offset=-1", "limit=0", "limit=101", "offset=oops"} {
		require.Equal(t, 400, harvestAdminRequest(r, "GET", "/nodes?"+query, "").Code)
	}
	require.Equal(t, 200, harvestAdminRequest(r, "GET", "/nodes", "").Code)
	nodes.fail = true
	require.Equal(t, 503, harvestAdminRequest(r, "GET", "/nodes", "").Code)
	require.Equal(t, 503, harvestAdminRequest(r, "POST", "/reset", `{"record_id":0}`).Code)
}

func TestManualHarvestHandlerValidatesBeforeStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ready := gin.New()
	ready.POST("/:id/manual-harvest", (&AccountHandler{openAIGatewayService: &service.OpenAIGatewayService{}}).ManualCodexHarvest)
	require.Equal(t, 400, harvestAdminRequest(ready, "POST", "/1/manual-harvest", `{"node_switch_rule":"nope"}`).Code)
	missing := gin.New()
	missing.POST("/:id/manual-harvest", (&AccountHandler{}).ManualCodexHarvest)
	require.Equal(t, 503, harvestAdminRequest(missing, "POST", "/1/manual-harvest", `{}`).Code)
}
