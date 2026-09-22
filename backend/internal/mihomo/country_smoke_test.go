package mihomo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Real Mihomo routes through a loopback fake upstream proxy, not a real airport
// or the public geolocation service. No model endpoint is contacted.
func TestCountryProbeWithOfficialKernel(t *testing.T) {
	if os.Getenv("MIHOMO_INSTALL_SMOKE") != "1" {
		t.Skip("opt-in official kernel download")
	}
	m := New(t.TempDir())
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	require.NoError(t, m.run(ctx, "install", saved{}))
	var country atomic.Value
	country.Store("HK")
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code, ok := country.Load().(string)
		if !ok {
			http.Error(w, "invalid mock country", 500)
			return
		}
		payload := fmt.Sprintf(`{"country":%q,"ip":"203.0.113.5"}`, code)
		if r.Method != http.MethodConnect {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(payload))
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
		request, err := http.ReadRequest(buffer.Reader)
		if err != nil {
			return
		}
		_ = request.Body.Close()
		reply := &http.Response{StatusCode: 200, ProtoMajor: 1, ProtoMinor: 1, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(payload)), ContentLength: int64(len(payload)), Close: true}
		_ = reply.Write(conn)
	}))
	defer proxy.Close()
	address, err := url.Parse(proxy.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(address.Port())
	require.NoError(t, err)
	node := map[string]any{"name": "node-test", "type": "http", "server": address.Hostname(), "port": port}
	m.countryLookupURL = "http://127.0.0.1:54321/country"
	observed := m.observeCountry(ctx, node)
	require.Equal(t, "HK", observed.Code)
	require.Empty(t, observed.Error)
	require.False(t, observed.CheckedAt.IsZero())
	// Apply exclusion to the real live group, then verify that a network request
	// cannot reach the fake HK exit. Turning the rule off restores that route.
	configured := saved{Nodes: []map[string]any{node}, Countries: map[string]CountryObservation{"node-test": observed}, CountryFilter: CountryFilter{Mode: "exclude", Codes: []string{"HK"}}}
	require.NoError(t, m.run(ctx, "start", configured))
	proxyURL, err := url.Parse(Endpoint)
	require.NoError(t, err)
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, m.countryLookupURL, nil)
	require.NoError(t, err)
	response, requestErr := client.Do(request)
	if requestErr == nil {
		require.NotEqual(t, http.StatusOK, response.StatusCode)
		_ = response.Body.Close()
	}
	configured = m.saved
	configured.CountryFilter = CountryFilter{Mode: "off"}
	require.NoError(t, m.run(ctx, "country_filter", configured))
	response, err = client.Do(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
	_ = response.Body.Close()
	country.Store("ZZ")
	invalid := m.observeCountry(ctx, node)
	require.Empty(t, invalid.Code)
	require.Equal(t, "lookup_failed", invalid.Error)
	results, err := m.scanCountries(ctx, saved{Nodes: []map[string]any{node}, Countries: map[string]CountryObservation{"node-test": observed}}, "node-test")
	require.NoError(t, err)
	require.Empty(t, results["node-test"].Code)
	require.Equal(t, "lookup_failed", results["node-test"].Error)
}
