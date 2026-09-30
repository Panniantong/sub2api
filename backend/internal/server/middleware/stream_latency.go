package middleware

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/streamlatency"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// StreamLatency observes ordinary HTTP inference without buffering or rewriting
// responses. GET websocket upgrades and non-inference endpoints are excluded.
func StreamLatency() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := strings.TrimRight(c.Request.URL.Path, "/")
		if c.Request.Method != http.MethodPost || !(strings.HasSuffix(path, "/responses") || strings.HasSuffix(path, "/chat/completions") || strings.HasSuffix(path, "/messages")) {
			c.Next()
			return
		}
		// Detailed diagnostics are opt-in through the existing debug log level.
		// Ordinary production traffic does not pay for a second SSE parser.
		if !logger.FromContext(c.Request.Context()).Core().Enabled(zap.DebugLevel) {
			c.Next()
			return
		}
		ctx, trace := streamlatency.NewContext(c.Request.Context())
		c.Request = c.Request.WithContext(ctx)
		writer := &streamLatencyWriter{ResponseWriter: c.Writer, trace: trace}
		writer.observer.OnOutput = trace.Output
		c.Writer = writer
		c.Next()
		if writer.streaming {
			logger.FromContext(ctx).Debug("gateway.http_stream_latency",
				zap.String("path", c.FullPath()),
				zap.Any("timing", trace.Snapshot()),
				zap.Bool("event_observation_limited", writer.observer.Limited))
		}
	}
}

type streamLatencyWriter struct {
	gin.ResponseWriter
	trace     *streamlatency.Trace
	observer  streamlatency.SSEObserver
	streaming bool
	failed    bool
}

func (w *streamLatencyWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *streamLatencyWriter) observe(data []byte, n int, err error) {
	if err != nil {
		w.failed = true
	}
	if !w.streaming {
		w.streaming = strings.HasPrefix(strings.ToLower(w.Header().Get("Content-Type")), "text/event-stream")
	}
	if w.streaming && n > 0 {
		w.observer.Feed(data[:n])
	}
}

func (w *streamLatencyWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	w.observe(data, n, err)
	return n, err
}

func (w *streamLatencyWriter) WriteString(data string) (int, error) {
	n, err := w.ResponseWriter.WriteString(data)
	w.observe([]byte(data), n, err)
	return n, err
}

func (w *streamLatencyWriter) Flush() {
	w.ResponseWriter.Flush()
	if !w.failed {
		w.trace.Flushed()
	}
}
