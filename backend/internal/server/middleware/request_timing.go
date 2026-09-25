package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/gin-gonic/gin"
)

// RequestTiming runs before authentication and body pre-read. WebSocket turns
// have a different lifetime and must not be labelled as an HTTP request trace.
func RequestTiming() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := strings.TrimRight(c.Request.URL.Path, "/")
		if c.Request.Method != http.MethodPost || (!strings.HasSuffix(path, "/responses") && !strings.HasSuffix(path, "/responses/compact")) {
			c.Next()
			return
		}
		collector := requesttiming.New(time.Now(), c.Request.ContentLength)
		c.Request = c.Request.WithContext(requesttiming.With(c.Request.Context(), collector))
		c.Request.Body = collector.WrapBody(c.Request.Body)
		writer := &timingWriter{ResponseWriter: c.Writer, collector: collector}
		c.Writer = writer
		defer func() { collector.Finish(writer.Status(), c.Request.Context().Err() != nil) }()
		c.Next()
	}
}

type timingWriter struct {
	gin.ResponseWriter
	collector *requesttiming.Collector
}

func (w *timingWriter) Write(p []byte) (int, error) {
	start := time.Now()
	n, err := w.ResponseWriter.Write(p)
	w.collector.Written(start, n, err, false)
	return n, err
}
func (w *timingWriter) WriteString(s string) (int, error) {
	start := time.Now()
	n, err := w.ResponseWriter.WriteString(s)
	w.collector.Written(start, n, err, false)
	return n, err
}
func (w *timingWriter) Flush() {
	start := time.Now()
	w.ResponseWriter.Flush()
	w.collector.Written(start, 0, nil, true)
}
func (w *timingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
