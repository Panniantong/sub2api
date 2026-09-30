package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestStreamLatencyHTTPClientReceivesFirstEventBeforeCompletion(t *testing.T) {
	for _, level := range []zap.AtomicLevel{zap.NewAtomicLevelAt(zap.InfoLevel), zap.NewAtomicLevelAt(zap.DebugLevel)} {
		for _, tc := range []struct{ path, first, terminal string }{
			{"/v1/responses", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"你好\"}\r\n\r\n", "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"output_tokens\":1}}}\r\n\r\n"},
			{"/v1/chat/completions", "data: {\"choices\":[{\"delta\":{\"content\":\"你好\"}}]}\n\n", "data: {\"choices\":[],\"usage\":{\"completion_tokens\":1}}\n\ndata: [DONE]\n\n"},
			{"/v1/messages", "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"你好\"}}\n\n", "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
		} {
			t.Run(level.String()+tc.path, func(t *testing.T) {
				core, logs := observer.New(level.Level())
				r := gin.New()
				r.Use(func(c *gin.Context) {
					c.Request = c.Request.WithContext(logger.IntoContext(c.Request.Context(), zap.New(core)))
					c.Next()
				})
				r.Use(StreamLatency())
				allowCompletion := make(chan struct{})
				finished := make(chan struct{})
				var once sync.Once
				release := func() { once.Do(func() { close(allowCompletion) }) }
				// Signal after the middleware has logged, to avoid racing assertions.
				handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					defer close(finished)
					r.ServeHTTP(w, req)
				})
				const heartbeat = ": keepalive\n\n"
				largeComment := ": " + strings.Repeat("x", 300<<10) + "\n\n"
				r.POST(tc.path, func(c *gin.Context) {
					c.Header("Content-Type", "text/event-stream; charset=utf-8")
					c.Header("X-Request-ID", "stream-review")
					_, _ = c.Writer.WriteString(heartbeat)
					c.Writer.Flush()
					// Split a complete event across Write and WriteString.
					_, _ = c.Writer.Write([]byte(tc.first[:13]))
					_, _ = c.Writer.WriteString(tc.first[13:])
					c.Writer.Flush()
					select {
					case <-allowCompletion:
					case <-c.Request.Context().Done():
						return
					}
					_, _ = c.Writer.WriteString(largeComment + tc.terminal)
					c.Writer.Flush()
				})
				srv := httptest.NewServer(handler)
				defer srv.Close()
				defer release()
				client := srv.Client()
				client.Timeout = 5 * time.Second
				resp, err := client.Post(srv.URL+tc.path, "application/json", strings.NewReader(`{"stream":true}`))
				require.NoError(t, err)
				defer resp.Body.Close()
				require.Equal(t, http.StatusOK, resp.StatusCode)
				require.Equal(t, "text/event-stream; charset=utf-8", resp.Header.Get("Content-Type"))
				require.Equal(t, "stream-review", resp.Header.Get("X-Request-ID"))
				first := make([]byte, len(heartbeat+tc.first))
				_, err = io.ReadFull(resp.Body, first)
				require.NoError(t, err, "first event must reach the client while completion is still gated")
				require.Equal(t, heartbeat+tc.first, string(first))
				release()
				rest, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.Equal(t, largeComment+tc.terminal, string(rest))
				select {
				case <-finished:
				case <-time.After(5 * time.Second):
					t.Fatal("request handler did not finish")
				}
				entries := logs.FilterMessage("gateway.http_stream_latency").All()
				if level.Level() == zap.DebugLevel {
					require.Len(t, entries, 1)
					timing := entries[0].ContextMap()["timing"].(map[string]any)
					require.Contains(t, timing, "first_output_ms")
					require.Contains(t, timing, "first_text_ms")
				} else {
					require.Empty(t, entries)
				}
			})
		}
	}
}

func TestStreamLatencyPreservesNonStreamingResponses(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/chat/completions", "/v1/messages"} {
		for _, status := range []int{http.StatusOK, http.StatusUnauthorized, http.StatusTooManyRequests} {
			core, logs := observer.New(zap.DebugLevel)
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Request = c.Request.WithContext(logger.IntoContext(c.Request.Context(), zap.New(core)))
				c.Next()
			})
			r.Use(StreamLatency())
			const body = `{"message":"unchanged","usage":{"total_tokens":7}}`
			r.POST(path, func(c *gin.Context) {
				c.Header("X-Request-ID", "json-review")
				c.Data(status, "application/json", []byte(body))
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"stream":false}`)))
			require.Equal(t, status, w.Code)
			require.Equal(t, body, w.Body.String())
			require.Equal(t, "application/json", w.Header().Get("Content-Type"))
			require.Equal(t, "json-review", w.Header().Get("X-Request-ID"))
			require.Empty(t, logs.FilterMessage("gateway.http_stream_latency").All())
		}
	}
}
