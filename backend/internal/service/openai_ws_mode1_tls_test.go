package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type mode1WSCapturedHello struct {
	ciphers   []uint16
	curves    []tls.CurveID
	protocols []string
}

// This endpoint performs a real TLS handshake and HTTP 101 upgrade. It only
// binds loopback and echoes the exact application frames received over WSS.
func newMode1WSTestEndpoint(t *testing.T) (*httptest.Server, <-chan mode1WSCapturedHello, *atomic.Int32) {
	t.Helper()
	hellos := make(chan mode1WSCapturedHello, 32)
	upgrades := &atomic.Int32{}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		upgrades.Add(1)
		defer conn.CloseNow()
		for {
			kind, payload, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if err := conn.Write(r.Context(), kind, payload); err != nil {
				return
			}
		}
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		hellos <- mode1WSCapturedHello{
			ciphers:   append([]uint16(nil), hello.CipherSuites...),
			curves:    append([]tls.CurveID(nil), hello.SupportedCurves...),
			protocols: append([]string(nil), hello.SupportedProtos...),
		}
		return nil, nil
	}}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server, hellos, upgrades
}

func mode1WSTestDialer(server *httptest.Server) *coderOpenAIWSClientDialer {
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	return &coderOpenAIWSClientDialer{tlsRootCAs: roots, proxyClients: make(map[string]*openAIWSProxyClientEntry)}
}

func TestMode1WSSActualHandshakeAndFramePreservation(t *testing.T) {
	server, hellos, upgrades := newMode1WSTestEndpoint(t)
	dialer := mode1WSTestDialer(server)
	profile := tlsfingerprint.BuiltinProfile("nodejs24")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, _, err := dialer.Dial(withOpenAIWSTLSProfile(ctx, 41, profile), "wss"+strings.TrimPrefix(server.URL, "https"), nil, "")
	require.NoError(t, err)
	defer conn.Close()
	select {
	case hello := <-hellos:
		require.Equal(t, profile.CipherSuites, hello.ciphers, "real ClientHello must use the configured cipher list and order")
		require.Equal(t, []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384}, hello.curves)
		require.Equal(t, []string{"http/1.1"}, hello.protocols)
	case <-ctx.Done():
		t.Fatal("no ClientHello captured")
	}
	frameConn := conn.(*coderOpenAIWSClientConn)
	for _, payload := range []string{
		`{"type":"response.create","model":"gpt-5.1","reasoning":{"effort":"high"},"input":[{"role":"user","content":"one"}],"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`,
		`{"type":"response.create","previous_response_id":"response-local","input":[{"type":"function_call_output","call_id":"call-local","output":"{\"answer\":42}"}]}`,
	} {
		require.NoError(t, frameConn.WriteFrame(ctx, coderws.MessageText, []byte(payload)))
		kind, response, err := frameConn.ReadFrame(ctx)
		require.NoError(t, err)
		require.Equal(t, coderws.MessageText, kind)
		require.Equal(t, payload, string(response), "TLS transport must not mutate application frames")
	}
	require.EqualValues(t, 1, upgrades.Load())
}

func TestMode1WSSTransportCacheIsolationAndProfileRotation(t *testing.T) {
	server, _, _ := newMode1WSTestEndpoint(t)
	dialer := mode1WSTestDialer(server)
	profile := tlsfingerprint.BuiltinProfile("nodejs24")
	cfg := openAIWSTLSConfig{accountID: 41, profile: profile}
	first, err := dialer.fingerprintHTTPClient(cfg, server.URL, "")
	require.NoError(t, err)
	same, err := dialer.fingerprintHTTPClient(cfg, server.URL, "")
	require.NoError(t, err)
	require.Same(t, first, same)
	otherAccount, err := dialer.fingerprintHTTPClient(openAIWSTLSConfig{accountID: 42, profile: profile}, server.URL, "")
	require.NoError(t, err)
	require.NotSame(t, first, otherAccount, "accounts must never share their transport cache")
	otherTarget, err := dialer.fingerprintHTTPClient(cfg, server.URL+"/other", "")
	require.NoError(t, err)
	require.NotSame(t, first, otherTarget)
	otherProxy, err := dialer.fingerprintHTTPClient(cfg, server.URL, "http://127.0.0.1:1")
	require.NoError(t, err)
	require.NotSame(t, first, otherProxy)
	changed := profile.Clone()
	changed.CipherSuites[0], changed.CipherSuites[1] = changed.CipherSuites[1], changed.CipherSuites[0]
	rotated, err := dialer.fingerprintHTTPClient(openAIWSTLSConfig{accountID: 41, profile: changed}, server.URL, "")
	require.NoError(t, err)
	require.NotSame(t, first, rotated, "same profile name with different contents must use a new transport")
}

func TestMode1WSSFailsClosedOnInvalidTLSAndCertificate(t *testing.T) {
	server, _, upgrades := newMode1WSTestEndpoint(t)
	target := "wss" + strings.TrimPrefix(server.URL, "https")
	t.Run("untrusted certificate", func(t *testing.T) {
		dialer := newDefaultOpenAIWSClientDialer()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, _, _, err := dialer.Dial(withOpenAIWSTLSProfile(ctx, 1, tlsfingerprint.BuiltinProfile("nodejs24")), target, nil, "")
		require.Error(t, err)
		require.Nil(t, conn)
		require.Contains(t, err.Error(), "certificate")
	})
	t.Run("invalid ALPN cannot fall back to ordinary TLS", func(t *testing.T) {
		dialer := mode1WSTestDialer(server)
		profile := tlsfingerprint.BuiltinProfile("nodejs24")
		profile.ALPNProtocols = []string{"h2"}
		conn, _, _, err := dialer.Dial(withOpenAIWSTLSProfile(context.Background(), 1, profile), target, nil, "")
		require.Error(t, err)
		require.Nil(t, conn)
	})
	t.Run("plaintext rejected", func(t *testing.T) {
		dialer := mode1WSTestDialer(server)
		conn, _, _, err := dialer.Dial(withOpenAIWSTLSProfile(context.Background(), 1, tlsfingerprint.BuiltinProfile("nodejs24")), "ws://127.0.0.1:1", nil, "")
		require.ErrorContains(t, err, "requires a valid wss target")
		require.Nil(t, conn)
	})
	require.Zero(t, upgrades.Load(), "no failure may result in an unprotected successful upgrade")
}

func TestMode1WSConnectionPoolTransportCompatibility(t *testing.T) {
	server, hellos, _ := newMode1WSTestEndpoint(t)
	cfg := &config.Config{}
	cfg.Gateway.TLSFingerprint.Enabled = true
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	pool := newOpenAIWSConnPool(cfg)
	defer pool.Close()
	pool.setClientDialerForTest(mode1WSTestDialer(server))
	profile := tlsfingerprint.BuiltinProfile("nodejs24")
	account := &Account{ID: 41, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1}
	req := openAIWSAcquireRequest{Account: account, WSURL: "wss" + strings.TrimPrefix(server.URL, "https"), tlsProfile: profile}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	first, err := pool.acquire(ctx, cloneOpenAIWSAcquireRequest(req), 0)
	require.NoError(t, err)
	firstID := first.ConnID()
	first.Release()
	reused, err := pool.acquire(ctx, cloneOpenAIWSAcquireRequest(req), 0)
	require.NoError(t, err)
	require.Equal(t, firstID, reused.ConnID())
	require.True(t, reused.Reused())
	reused.Release()
	changed := profile.Clone()
	changed.CipherSuites[0], changed.CipherSuites[1] = changed.CipherSuites[1], changed.CipherSuites[0]
	req.tlsProfile = changed
	rotated, err := pool.acquire(ctx, cloneOpenAIWSAcquireRequest(req), 0)
	require.NoError(t, err)
	require.NotEqual(t, firstID, rotated.ConnID())
	require.False(t, rotated.Reused())
	rotated.Release()
	req.WSURL += "/other"
	otherTarget, err := pool.acquire(ctx, cloneOpenAIWSAcquireRequest(req), 0)
	require.NoError(t, err)
	require.NotEqual(t, rotated.ConnID(), otherTarget.ConnID())
	otherTarget.Release()
	otherAccount := *account
	otherAccount.ID = 42
	req.Account = &otherAccount
	other, err := pool.acquire(ctx, cloneOpenAIWSAcquireRequest(req), 0)
	require.NoError(t, err)
	require.NotEqual(t, otherTarget.ConnID(), other.ConnID())
	other.Release()
	for _, expected := range [][]uint16{profile.CipherSuites, changed.CipherSuites, changed.CipherSuites, changed.CipherSuites} {
		select {
		case hello := <-hellos:
			require.Equal(t, expected, hello.ciphers)
		case <-ctx.Done():
			t.Fatal("missing handshake after account/target/profile change")
		}
	}
}

func mode1WSAccount(id int64) *Account {
	return &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 2, Extra: map[string]any{
		"anti_degrade":           map[string]any{"enabled": true, "mode": "mode1", "policy_version": 2, "max_concurrency": 2},
		"codex_fingerprint_mode": "device", "codex_fingerprint_seed": testCodexFingerprintSeed,
		"enable_tls_fingerprint": true, "tls_fingerprint_builtin": "nodejs24",
	}}
}

func TestMode1WSAcquireUsesAccountTLSAndKeepsSessionsIsolated(t *testing.T) {
	server, hellos, upgrades := newMode1WSTestEndpoint(t)
	cfg := &config.Config{}
	cfg.Gateway.TLSFingerprint.Enabled = true
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 2
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 2
	pool := newOpenAIWSConnPool(cfg)
	defer pool.Close()
	pool.setClientDialerForTest(mode1WSTestDialer(server))
	req := openAIWSAcquireRequest{
		Account: mode1WSAccount(43), WSURL: "wss" + strings.TrimPrefix(server.URL, "https"),
		Headers: http.Header{"Session_id": {"session-a"}, "Thread-Id": {"thread-a"}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	first, err := pool.Acquire(ctx, req)
	require.NoError(t, err)
	requireMode1WSEcho(t, ctx, first)
	first.Release()
	req.Headers = http.Header{"Session_id": {"session-b"}, "Thread-Id": {"thread-b"}}
	second, err := pool.Acquire(ctx, req)
	require.NoError(t, err)
	requireMode1WSEcho(t, ctx, second)
	require.NotEqual(t, first.ConnID(), second.ConnID(), "different conversations must not reuse one upstream WSS session")
	second.Release()
	for i := 0; i < 2; i++ {
		select {
		case hello := <-hellos:
			require.Equal(t, tlsfingerprint.BuiltinProfile("nodejs24").CipherSuites, hello.ciphers)
		case <-ctx.Done():
			t.Fatal("account policy did not reach a real TLS dial")
		}
	}
	require.EqualValues(t, 2, upgrades.Load())
	// A globally disabled template must attempt standard TLS rather than fail
	// locally. The standard client correctly rejects this private test CA.
	cfg.Gateway.TLSFingerprint.Enabled = false
	lease, err := pool.Acquire(ctx, req)
	require.ErrorContains(t, err, "certificate")
	require.Nil(t, lease)
	cfg.Gateway.TLSFingerprint.Enabled = true
	req.Account.Extra["enable_tls_fingerprint"] = false
	lease, err = pool.Acquire(ctx, req)
	require.ErrorContains(t, err, "certificate")
	require.Nil(t, lease)
	require.EqualValues(t, 2, upgrades.Load())
}

// A successful dial can return as soon as Accept flushes HTTP 101, before the
// server increments its upgrade counter. A real frame round trip establishes
// that server-side barrier without sleeps or weakening the connection check.
func requireMode1WSEcho(t *testing.T, ctx context.Context, lease *openAIWSConnLease) {
	t.Helper()
	require.NoError(t, lease.WriteJSONContext(ctx, map[string]any{"type": "test.echo", "probe": "ready"}))
	body, err := lease.ReadMessageContext(ctx)
	require.NoError(t, err)
	require.Contains(t, string(body), `"probe":"ready"`)
}

func TestMode1WSRuntimeConcurrencyCapCannotBeBypassed(t *testing.T) {
	for _, router := range []bool{false, true} {
		for _, dynamic := range []bool{false, true} {
			cfg := &config.Config{}
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 100
			cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = router
			cfg.Gateway.OpenAIWS.DynamicMaxConnsByAccountConcurrencyEnabled = dynamic
			cfg.Gateway.OpenAIWS.OAuthMaxConnsFactor = 4
			pool := newOpenAIWSConnPool(cfg)
			account := mode1WSAccount(44)
			account.Concurrency = 50
			require.Equal(t, 50, pool.effectiveMaxConnsByAccount(account))
			account.Concurrency = 0
			require.Equal(t, AntiDegradeConcurrencyCap, pool.effectiveMaxConnsByAccount(account))
			account.Concurrency = 1
			require.Equal(t, 1, pool.effectiveMaxConnsByAccount(account))
			pool.Close()
		}
	}
}

type mode1WSLocalTargetDialer struct {
	inner    *coderOpenAIWSClientDialer
	target   string
	observed chan openAIWSTLSConfig
	headers  chan http.Header
}

func (d *mode1WSLocalTargetDialer) Dial(ctx context.Context, _ string, h http.Header, proxy string) (openAIWSClientConn, int, http.Header, error) {
	if cfg, ok := ctx.Value(openAIWSTLSContextKey{}).(openAIWSTLSConfig); ok {
		d.observed <- cfg
	}
	if d.headers != nil {
		d.headers <- h.Clone()
	}
	// Test-only retargeting: no production/upstream host is ever contacted.
	return d.inner.Dial(ctx, d.target, h, proxy)
}

func TestMode1WSPassthroughEntryReachesRealTLS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream, hellos, _ := newMode1WSTestEndpoint(t)
	dialer := &mode1WSLocalTargetDialer{
		inner: mode1WSTestDialer(upstream), target: "wss" + strings.TrimPrefix(upstream.URL, "https"),
		observed: make(chan openAIWSTLSConfig, 1), headers: make(chan http.Header, 1),
	}
	cfg := passthroughLifecycleConfig()
	cfg.Gateway.TLSFingerprint.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	account := mode1WSAccount(45)
	account.Credentials = map[string]any{"chatgpt_account_id": "00000000-0000-4000-8000-000000000045"}
	account.Extra["openai_oauth_responses_websockets_v2_mode"] = OpenAIWSIngressModePassthrough
	svc := newPassthroughLifecycleService(cfg, newStagedPassthroughConn())
	svc.openaiWSPassthroughDialer = dialer
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	server, serverErr := startPassthroughLifecycleServer(t, ctx, svc, account)
	defer server.Close()
	client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"gpt-5.1","input":[{"role":"user","content":"local-only"}],"reasoning":{"effort":"high"}}`)
	defer client.CloseNow()
	select {
	case observed := <-dialer.observed:
		require.Equal(t, account.ID, observed.accountID)
		require.Equal(t, tlsfingerprint.BuiltinProfile("nodejs24").CacheKey(), observed.profile.CacheKey())
	case err := <-serverErr:
		t.Fatalf("passthrough failed before TLS: %v", err)
	case <-ctx.Done():
		t.Fatal("passthrough did not dial")
	}
	actualWSHeaders := <-dialer.headers
	httpContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	httpContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	httpRequest, err := svc.buildUpstreamRequest(ctx, httpContext, account, []byte(`{"model":"gpt-5.4","input":"local-only"}`), "sk-test", false, "", false)
	require.NoError(t, err)
	require.NotEmpty(t, actualWSHeaders.Get("x-codex-installation-id"))
	require.Equal(t, httpRequest.Header.Get("x-codex-installation-id"), actualWSHeaders.Get("x-codex-installation-id"), "native WSS and HTTP must derive the same device from the persistent account seed")
	select {
	case hello := <-hellos:
		require.Equal(t, tlsfingerprint.BuiltinProfile("nodejs24").CipherSuites, hello.ciphers)
	case <-ctx.Done():
		t.Fatal("passthrough did not perform TLS")
	}
	_, payload, err := client.Read(ctx)
	require.NoError(t, err)
	require.Contains(t, string(payload), `"effort":"high"`)
	require.Contains(t, string(payload), `"local-only"`)
	require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
	select {
	case <-serverErr:
	case <-ctx.Done():
		t.Fatal("passthrough did not close")
	}
}

func TestMode1WSIngressRejectsLossyPolicyBeforeDial(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{OpenAIWSIngressModePassthrough, OpenAIWSIngressModeCtxPool} {
		t.Run(mode, func(t *testing.T) {
			upstream, _, upgrades := newMode1WSTestEndpoint(t)
			dialer := &mode1WSLocalTargetDialer{
				inner: mode1WSTestDialer(upstream), target: "wss" + strings.TrimPrefix(upstream.URL, "https"),
				observed: make(chan openAIWSTLSConfig, 4),
			}
			cfg := passthroughLifecycleConfig()
			cfg.Gateway.TLSFingerprint.Enabled = true
			cfg.Gateway.OpenAIWS.OAuthEnabled = true
			account := mode1WSAccount(46)
			account.Credentials = map[string]any{"chatgpt_account_id": "00000000-0000-4000-8000-000000000046"}
			account.Extra["openai_oauth_responses_websockets_v2_mode"] = mode
			svc := newPassthroughLifecycleService(cfg, newStagedPassthroughConn())
			svc.openaiWSPassthroughDialer = dialer
			svc.getOpenAIWSConnPool().setClientDialerForTest(dialer)
			defer svc.openaiWSPool.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(*gin.Context) *OpenAIWSIngressHooks {
				return &OpenAIWSIngressHooks{MaxReasoningEffort: "low", MaxReasoningEffortOverLimit: "downgrade"}
			})
			defer server.Close()
			client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"gpt-5.4","input":[{"role":"user","content":"local-only"}],"reasoning":{"effort":"high"}}`)
			defer client.CloseNow()
			select {
			case err := <-serverErr:
				require.ErrorContains(t, err, "reasoning")
			case <-ctx.Done():
				t.Fatal("lossy policy was not rejected")
			}
			require.Zero(t, upgrades.Load())
			require.Empty(t, dialer.observed, "lossy request must not reach any dial path")
		})
	}
}

func TestMode1WSSOverLocalHTTPAndHTTPSProxy(t *testing.T) {
	for _, useTLS := range []bool{false, true} {
		name := "http"
		if useTLS {
			name = "https"
		}
		t.Run(name, func(t *testing.T) {
			upstream, hellos, _ := newMode1WSTestEndpoint(t)
			targetAddr := strings.TrimPrefix(upstream.URL, "https://")
			proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodConnect || r.Host != targetAddr {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				peer, err := (&net.Dialer{}).DialContext(r.Context(), "tcp", targetAddr)
				if err != nil {
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				client, rw, err := w.(http.Hijacker).Hijack()
				if err != nil {
					peer.Close()
					return
				}
				defer client.Close()
				defer peer.Close()
				if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
					return
				}
				done := make(chan struct{})
				go func() { _, _ = io.Copy(peer, rw); _ = peer.Close(); close(done) }()
				_, _ = io.Copy(client, peer)
				_ = client.Close()
				<-done
			}))
			if useTLS {
				proxy.StartTLS()
			} else {
				proxy.Start()
			}
			defer proxy.Close()
			dialer := mode1WSTestDialer(upstream)
			if useTLS {
				dialer.tlsProxyRootCAs = x509.NewCertPool()
				dialer.tlsProxyRootCAs.AddCert(proxy.Certificate())
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			profile := tlsfingerprint.BuiltinProfile("nodejs24")
			conn, _, _, err := dialer.Dial(withOpenAIWSTLSProfile(ctx, 47, profile), "wss://"+targetAddr, nil, proxy.URL)
			require.NoError(t, err)
			defer conn.Close()
			require.NoError(t, conn.WriteJSON(ctx, map[string]any{"type": "response.create", "input": "proxy-local-only"}))
			payload, err := conn.ReadMessage(ctx)
			require.NoError(t, err)
			require.Contains(t, string(payload), "proxy-local-only")
			select {
			case hello := <-hellos:
				require.Equal(t, profile.CipherSuites, hello.ciphers, "the destination must see uTLS through the CONNECT tunnel")
			case <-ctx.Done():
				t.Fatal("no proxied ClientHello")
			}
		})
	}
}
