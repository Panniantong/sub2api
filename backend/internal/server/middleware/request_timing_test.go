package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/gin-gonic/gin"
)

func TestRequestTimingPreReadAndHandlerRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestTiming())
	var got requesttiming.Snapshot
	r.Use(func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			t.Fatal(err)
		}
		c.Request.Body = io.NopCloser(strings.NewReader(string(body)))
		c.Next()
	})
	r.POST("/v1/responses", func(c *gin.Context) {
		_, _ = io.ReadAll(c.Request.Body)
		requesttiming.From(c.Request.Context()).WhenFinished(func(s requesttiming.Snapshot) { got = s })
		c.String(200, "ok")
		c.Writer.Flush()
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader("abc")))
	if got.BodyBytes != 3 || !got.BodyComplete || got.DownstreamBytes != 2 || got.Status != 200 {
		t.Fatalf("bad trace %+v", got)
	}
}
