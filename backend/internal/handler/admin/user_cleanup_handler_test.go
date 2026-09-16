package admin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUserCleanupHandlerDeniesUnauthorizedRoles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, role := range []string{"", "user", "system"} {
		for _, endpoint := range []string{"summary", "preview", "execute", "get-guard", "put-guard"} {
			t.Run(role+"/"+endpoint, func(t *testing.T) {
				h := NewUserCleanupHandler(nil)
				r := gin.New()
				if role != "" {
					r.Use(func(c *gin.Context) {
						c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
						c.Set(string(middleware.ContextKeyUserRole), role)
					})
				}
				handlers := map[string]gin.HandlerFunc{"summary": h.Summary, "preview": h.Preview, "execute": h.Execute, "get-guard": h.GetGuard, "put-guard": h.UpdateGuard}
				r.POST("/test", handlers[endpoint])
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/test", nil))
				status := http.StatusForbidden
				if role == "" {
					status = http.StatusUnauthorized
				}
				require.Equal(t, status, rec.Code)
			})
		}
	}
}

func TestUserCleanupHandlerRequiresConfirmation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
	})
	h := NewUserCleanupHandler(nil)
	r.POST("/execute", h.Execute)
	for _, body := range []string{`{}`, `{"preview_id":"xxxxxxxx","confirm":true,"expected_count":1}`, `{"preview_id":"11111111-1111-4111-8111-111111111111","confirm":false,"expected_count":1}`, `{"preview_id":"11111111-1111-4111-8111-111111111111","confirm":true,"expected_count":501}`} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/execute", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	}
}
