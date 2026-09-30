package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/streamlatency"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type failingStreamLatencyWriter struct {
	gin.ResponseWriter
	err     error
	flushes int
}

func (w *failingStreamLatencyWriter) Write(p []byte) (int, error) {
	n, _ := w.ResponseWriter.Write(p)
	return n, w.err
}

func (w *failingStreamLatencyWriter) WriteString(p string) (int, error) {
	return w.Write([]byte(p))
}

func (w *failingStreamLatencyWriter) Flush() {
	w.flushes++
	w.ResponseWriter.Flush()
}

func TestStreamLatencyPreservesWriteErrorsWithoutCountingSuccessfulOutput(t *testing.T) {
	for _, useString := range []bool{false, true} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		_, trace := streamlatency.NewContext(context.Background())
		base := &failingStreamLatencyWriter{ResponseWriter: c.Writer, err: errors.New("client disconnected")}
		w := &streamLatencyWriter{ResponseWriter: base, trace: trace}
		w.observer.OnOutput = trace.Output
		w.Header().Set("Content-Type", "text/event-stream")
		const payload = "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"
		var n int
		var err error
		if useString {
			n, err = w.WriteString(payload)
		} else {
			n, err = w.Write([]byte(payload))
		}
		require.Equal(t, len(payload), n)
		require.ErrorIs(t, err, base.err)
		w.Flush()
		require.Equal(t, 1, base.flushes)
		require.Equal(t, payload, recorder.Body.String())
		require.NotContains(t, trace.Snapshot(), "first_output_ms")
		require.NotContains(t, trace.Snapshot(), "first_text_ms")
	}
}

func TestStreamLatencyPreservesSSEAndCountsOnlyFlushedContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	core, _ := observer.New(zap.DebugLevel)
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(logger.IntoContext(c.Request.Context(), zap.New(core)))
		c.Next()
	})
	r.Use(StreamLatency())
	const heartbeat = ": keepalive\n\n"
	const data = "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		trace := streamlatency.FromContext(c.Request.Context())
		require.NotNil(t, trace)
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.WriteString(heartbeat)
		c.Writer.Flush()
		require.NotContains(t, trace.Snapshot(), "first_output_ms")
		_, _ = c.Writer.Write([]byte(data[:10]))
		c.Writer.Flush()
		require.NotContains(t, trace.Snapshot(), "first_output_ms")
		_, _ = c.Writer.WriteString(data[10:])
		require.NotContains(t, trace.Snapshot(), "first_output_ms")
		c.Writer.Flush()
		require.Contains(t, trace.Snapshot(), "first_text_ms")
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, heartbeat+data, w.Body.String())
}

func TestStreamLatencySkipsWebsocketAndNonInferenceRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	core, _ := observer.New(zap.DebugLevel)
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(logger.IntoContext(c.Request.Context(), zap.New(core)))
		c.Next()
	})
	r.Use(StreamLatency())
	h := func(c *gin.Context) {
		require.Nil(t, streamlatency.FromContext(c.Request.Context()))
		c.Status(http.StatusNoContent)
	}
	r.GET("/v1/responses", h)
	r.POST("/v1/responses/compact", h)
	for _, tc := range []struct{ method, path string }{{http.MethodGet, "/v1/responses"}, {http.MethodPost, "/v1/responses/compact"}} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		require.Equal(t, http.StatusNoContent, w.Code)
	}
}

func TestStreamLatencyDisabledOutsideDebug(t *testing.T) {
	r := gin.New()
	core, _ := observer.New(zap.InfoLevel)
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(logger.IntoContext(c.Request.Context(), zap.New(core)))
		c.Next()
	})
	r.Use(StreamLatency())
	r.POST("/v1/responses", func(c *gin.Context) {
		require.Nil(t, streamlatency.FromContext(c.Request.Context()))
		_, wrapped := c.Writer.(*streamLatencyWriter)
		require.False(t, wrapped)
		c.Status(http.StatusNoContent)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	require.Equal(t, http.StatusNoContent, w.Code)
}
