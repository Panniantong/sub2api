package mihomo

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCollectionWithOfficialKernel(t *testing.T) {
	if os.Getenv("MIHOMO_INSTALL_SMOKE") != "1" {
		t.Skip("opt-in official kernel download")
	}
	fake := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodConnect {
				_, _ = io.WriteString(w, name)
				return
			}
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "hijack unavailable", 500)
				return
			}
			conn, buffer, err := hijacker.Hijack()
			if err != nil {
				return
			}
			defer func() { _ = conn.Close() }()
			_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
			if buffer.Flush() != nil {
				return
			}
			req, err := http.ReadRequest(buffer.Reader)
			if err != nil {
				return
			}
			_ = req.Body.Close()
			resp := &http.Response{StatusCode: 200, ProtoMajor: 1, ProtoMinor: 1, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(name)), ContentLength: int64(len(name)), Close: true}
			_ = resp.Write(conn)
		}))
	}
	first := fake("first")
	defer first.Close()
	second := fake("second")
	defer second.Close()
	node := func(name, address string) map[string]any {
		u, err := url.Parse(address)
		require.NoError(t, err)
		port, err := strconv.Atoi(u.Port())
		require.NoError(t, err)
		return map[string]any{"name": name, "type": "http", "server": u.Hostname(), "port": port}
	}
	m := New(t.TempDir())
	defer m.Close()
	// The opt-in smoke test downloads a release asset before local probes.
	m.client.Timeout = 90 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	require.NoError(t, m.run(ctx, "install", saved{}))
	require.NoError(t, m.run(ctx, "start", saved{UseOnce: true, Nodes: []map[string]any{node("first", first.URL), node("second", second.URL)}}))
	c, err := BeginCollection(ctx, Endpoint)
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	n1, p1, err := c.Next(ctx, 0)
	require.NoError(t, err)
	n2, p2, err := c.Next(ctx, 1)
	require.NoError(t, err)
	fetch := func(proxy string) string {
		u, err := url.Parse(proxy)
		require.NoError(t, err)
		transport := &http.Transport{Proxy: http.ProxyURL(u)}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:54321/probe", nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return string(body)
	}
	require.Equal(t, n1, fetch(p1))
	require.Equal(t, n2, fetch(p2))
	require.Equal(t, n1, fetch(p1))
	require.NoError(t, c.Close())
	proxy, release, err := PinNode(ctx, n1)
	require.NoError(t, err)
	defer release()
	require.Equal(t, n1, fetch(proxy))
	release()
	// Exercise the directed adapter against the real selector/listener API,
	// with local simulated exits in both reusable and use-once modes.
	for _, useOnce := range []bool{false, true} {
		require.NoError(t, m.run(ctx, "start", saved{Secret: m.saved.Secret, UseOnce: useOnce, Nodes: []map[string]any{node("first", first.URL), node("second", second.URL)}}))
		sidecar, err := LoadDirectedSidecar("", Endpoint)
		require.NoError(t, err)
		nodes, err := sidecar.Directory(ctx)
		require.NoError(t, err)
		for _, selected := range nodes {
			end, err := sidecar.Acquire(ctx, selected)
			require.NoError(t, err)
			require.Equal(t, selected.ID, fetch(sidecar.ProxyURL))
			require.NoError(t, sidecar.Confirm(ctx, selected))
			end()
		}
		if useOnce {
			_, err := sidecar.Directory(ctx)
			require.ErrorContains(t, err, "no eligible")
		}
	}
}
