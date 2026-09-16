package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSystemMutationsRejectOrdinaryAdminBeforeSideEffects(t *testing.T) {
	for _, method := range []gin.HandlerFunc{(&SystemHandler{}).PerformUpdate, (&SystemHandler{}).Rollback, (&SystemHandler{}).RestartService} {
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin) })
		r.POST("/mutation", method)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/mutation", nil))
		require.Equal(t, 403, w.Code)
	}
}
