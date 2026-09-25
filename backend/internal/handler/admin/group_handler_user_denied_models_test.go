package admin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newUserDeniedModelsGroupRouter(svc *stubAdminService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewGroupHandlerWithConfig(svc, nil, nil, &config.Config{})
	r := gin.New()
	r.PUT("/groups/:id/user-denied-models", h.BatchSetGroupUserDeniedModels)
	r.DELETE("/groups/:id/user-denied-models", h.ClearGroupUserDeniedModels)
	return r
}

func TestGroupHandlerBatchSetUserDeniedModels(t *testing.T) {
	svc := newStubAdminService()
	r := newUserDeniedModelsGroupRouter(svc)

	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/groups/5/user-denied-models",
		bytes.NewBufferString(`{"entries":[{"user_id":7,"denied_models":["gpt-6-luna"]},{"user_id":8,"denied_models":[]}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(res, req)

	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	require.Equal(t, int64(5), svc.userDeniedModelsGroupID)
	require.Equal(t, []service.GroupUserDeniedModelsInput{
		{UserID: 7, DeniedModels: []string{"gpt-6-luna"}},
		{UserID: 8, DeniedModels: []string{}},
	}, svc.userDeniedModelsEntries)
}

func TestGroupHandlerBatchSetUserDeniedModelsRejectsMissingEntries(t *testing.T) {
	svc := newStubAdminService()
	r := newUserDeniedModelsGroupRouter(svc)

	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/groups/5/user-denied-models", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(res, req)

	require.Equal(t, http.StatusBadRequest, res.Code)
	require.Nil(t, svc.userDeniedModelsEntries)
}

func TestGroupHandlerClearUserDeniedModels(t *testing.T) {
	svc := newStubAdminService()
	r := newUserDeniedModelsGroupRouter(svc)

	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodDelete, "/groups/5/user-denied-models", nil))

	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	require.Equal(t, 1, svc.clearUserDeniedModelsCalls)
	require.Equal(t, int64(5), svc.userDeniedModelsGroupID)
}
