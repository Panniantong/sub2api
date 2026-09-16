package repository

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestMode1TLSPoolAlwaysSeparatesAccountsProxyAndProfile(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ConnectionPoolIsolation = config.ConnectionPoolIsolationProxy
	svc := NewHTTPUpstream(cfg).(*httpUpstreamService)
	p := tlsfingerprint.BuiltinProfile("nodejs24")
	get := func(account int64, proxy string, p *tlsfingerprint.Profile) *upstreamClientEntry {
		entry, err := svc.getClientEntryWithTLS(proxy, account, 2, p, service.HTTPUpstreamProfileOpenAI, false, false)
		require.NoError(t, err)
		t.Cleanup(entry.client.CloseIdleConnections)
		return entry
	}
	one := get(1, "", p)
	require.Same(t, one, get(1, "", p.Clone()), "stable profile should reuse account pool")
	require.NotSame(t, one, get(2, "", p), "global proxy mode must not share fingerprint pools across accounts")
	require.NotSame(t, one, get(1, "http://127.0.0.1:1080", p), "proxy update must select a new pool")
	changed := p.Clone()
	changed.CipherSuites[0], changed.CipherSuites[1] = changed.CipherSuites[1], changed.CipherSuites[0]
	require.NotSame(t, one, get(1, "", changed), "same-name template edit must select a new pool")
}

func TestMode1DoWithTLSActuallyUsesClientHello(t *testing.T) {
	var handshakes atomic.Int64
	p := tlsfingerprint.BuiltinProfile("nodejs24")
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(w, r.Body)
	}))
	target.TLS = &tls.Config{GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
		handshakes.Add(1)
		if !reflect.DeepEqual(h.CipherSuites, p.CipherSuites) {
			t.Errorf("actual upstream ClientHello differs: got %v want %v", h.CipherSuites, p.CipherSuites)
		}
		return nil, nil
	}}
	target.StartTLS()
	t.Cleanup(target.Close)
	roots := x509.NewCertPool()
	roots.AddCert(target.Certificate())
	svc := NewHTTPUpstream(nil).(*httpUpstreamService)
	entry, err := svc.getClientEntryWithTLS("", 19, 2, p, service.HTTPUpstreamProfileDefault, false, false)
	require.NoError(t, err)
	// Inject only the local CA into the already selected production transport.
	// DoWithTLS still resolves its own account/profile cache entry and executes
	// the complete request path, with certificate verification enabled.
	transport := entry.client.Transport.(*http.Transport)
	require.NoError(t, tlsfingerprint.ConfigureTransport(transport, p, nil, tlsfingerprint.TransportOptions{RootCAs: roots}))
	t.Cleanup(transport.CloseIdleConnections)
	const payload = `{"reasoning":{"effort":"high"},"tools":[{"type":"function","name":"test"}],"input":"complete"}`
	req, err := http.NewRequest(http.MethodPost, target.URL, strings.NewReader(payload))
	require.NoError(t, err)
	resp, err := svc.DoWithTLS(req, "", 19, 2, p)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, payload, string(body))
	require.Equal(t, int64(1), handshakes.Load())
	require.Zero(t, atomic.LoadInt64(&entry.inFlight))
}
