package repository

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/streamlatency"
	"github.com/stretchr/testify/require"
)

func TestHTTPUpstreamCPAFirstByteSurvivesDecompression(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "gzip"}[compressed], func(t *testing.T) {
			const body = "data: {\"type\":\"response.created\"}\n\n"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(25 * time.Millisecond)
				w.Header().Set("Content-Type", "text/event-stream")
				if compressed {
					w.Header().Set("Content-Encoding", "gzip")
					gz := gzip.NewWriter(w)
					_, _ = io.WriteString(gz, body)
					_ = gz.Close()
				} else {
					_, _ = io.WriteString(w, body)
				}
			}))
			defer srv.Close()
			svc := NewHTTPUpstream(&config.Config{}).(*httpUpstreamService)
			defer func() {
				for _, entry := range svc.clients {
					entry.client.CloseIdleConnections()
				}
			}()
			req, err := http.NewRequest("POST", srv.URL, nil)
			require.NoError(t, err)
			// Force repository decompression instead of net/http's implicit gzip.
			req.Header.Set("Accept-Encoding", "gzip")
			resp, err := svc.Do(streamlatency.EnableFirstByte(req), "", 1, 1)
			require.NoError(t, err)
			got, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, body, string(got))
			ms, tracked := streamlatency.FirstByteMilliseconds(resp)
			require.True(t, tracked)
			require.NotNil(t, ms)
			require.GreaterOrEqual(t, *ms, 25)
		})
	}
}
