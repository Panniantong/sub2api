package admin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func serveGroupRequest(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	return res
}

func TestGroupHandlerPassesStreamOnlyToService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStubAdminService()
	h := NewGroupHandlerWithConfig(svc, nil, nil, &config.Config{})
	r := gin.New()
	r.POST("/groups", h.Create)
	r.PUT("/groups/:id", h.Update)

	res := serveGroupRequest(r, http.MethodPost, "/groups", `{"name":"cc","platform":"anthropic","rate_multiplier":1,"stream_only":true}`)
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	require.Len(t, svc.createdGroups, 1)
	require.True(t, svc.createdGroups[0].StreamOnly)

	res = serveGroupRequest(r, http.MethodPut, "/groups/2", `{"stream_only":false}`)
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	res = serveGroupRequest(r, http.MethodPut, "/groups/2", `{"name":"renamed"}`)
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	require.Len(t, svc.updatedGroups, 2)
	require.NotNil(t, svc.updatedGroups[0].StreamOnly)
	require.False(t, *svc.updatedGroups[0].StreamOnly)
	require.Nil(t, svc.updatedGroups[1].StreamOnly, "没带 stream_only 时不改")
}

func TestGroupHandlerSimpleModeDropsStreamOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStubAdminService()
	r := newSimpleModeGroupRouter(svc)

	res := serveGroupRequest(r, http.MethodPost, "/groups", `{"name":"simple","platform":"anthropic","stream_only":true}`)
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	res = serveGroupRequest(r, http.MethodPut, "/groups/2", `{"name":"simple","stream_only":true}`)
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	require.False(t, svc.createdGroups[0].StreamOnly)
	require.Nil(t, svc.updatedGroups[0].StreamOnly)
}
