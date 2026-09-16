package tlsfingerprint

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type localHello struct {
	ciphers []uint16
	curves  []tls.CurveID
	alpn    []string
}

func localTLSTarget(t *testing.T) (*httptest.Server, *x509.CertPool, chan localHello, *atomic.Int64) {
	t.Helper()
	hellos := make(chan localHello, 16)
	calls := &atomic.Int64{}
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(w, r.Body)
	}))
	s.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		hellos <- localHello{append([]uint16(nil), hello.CipherSuites...), append([]tls.CurveID(nil), hello.SupportedCurves...), append([]string(nil), hello.SupportedProtos...)}
		return nil, nil
	}}
	s.StartTLS()
	t.Cleanup(s.Close)
	roots := x509.NewCertPool()
	roots.AddCert(s.Certificate())
	return s, roots, hellos, calls
}

func assertLocalHello(t *testing.T, got localHello, p *Profile) {
	t.Helper()
	if !reflect.DeepEqual(got.ciphers, p.CipherSuites) {
		t.Fatalf("actual ClientHello cipher list differs from profile: got %v want %v", got.ciphers, p.CipherSuites)
	}
	curves := make([]tls.CurveID, len(p.Curves))
	for i, curve := range p.Curves {
		curves[i] = tls.CurveID(curve)
	}
	if !reflect.DeepEqual(got.curves, curves) || !reflect.DeepEqual(got.alpn, []string{"http/1.1"}) {
		t.Fatalf("actual ClientHello curves/ALPN differ: %+v", got)
	}
}

func localRoundTrip(t *testing.T, transport *http.Transport, target string) {
	t.Helper()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	const payload = `{"model":"test-model","reasoning":{"effort":"high"},"input":[{"role":"user","content":"complete input"}]}`
	resp, err := client.Post(target, "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != payload || resp.StatusCode != http.StatusOK {
		t.Fatalf("TLS round trip changed payload: status=%d body=%q err=%v", resp.StatusCode, body, err)
	}
}

func TestLocalTLSHTTPActualHelloAndImmutableProfile(t *testing.T) {
	target, roots, hellos, calls := localTLSTarget(t)
	p := BuiltinProfile("nodejs24")
	expected := p.Clone()
	transport, err := NewHTTPTransport(p, nil, TransportOptions{RootCAs: roots})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.CloseIdleConnections)
	p.CipherSuites[0] = 0xffff
	p.Curves[0] = 0xffff
	localRoundTrip(t, transport, target.URL)
	localRoundTrip(t, transport, target.URL)
	assertLocalHello(t, <-hellos, expected)
	if len(hellos) != 0 || calls.Load() != 2 {
		t.Fatal("same transport did not reuse the verified TLS connection")
	}
}

func TestLocalTLSRejectsUntrustedCertificate(t *testing.T) {
	target, _, hellos, calls := localTLSTarget(t)
	transport, err := NewHTTPTransport(BuiltinProfile("nodejs24"), nil, TransportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.CloseIdleConnections)
	_, err = (&http.Client{Transport: transport, Timeout: 3 * time.Second}).Get(target.URL)
	if err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("untrusted certificate must be rejected: %v", err)
	}
	if calls.Load() != 0 || len(hellos) != 1 {
		t.Fatal("untrusted certificate triggered application traffic or a fallback attempt")
	}
}

func localConnectProxy(t *testing.T, secure bool) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	calls := &atomic.Int64{}
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodConnect || r.Header.Get("Proxy-Authorization") != "Basic dGVzdDp0ZXN0" {
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		upstream, err := net.DialTimeout("tcp", r.Host, time.Second)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		downstream, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		_, _ = io.WriteString(downstream, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go func() {
			defer upstream.Close()
			defer downstream.Close()
			_, _ = io.Copy(upstream, downstream)
		}()
		go func() {
			defer upstream.Close()
			defer downstream.Close()
			_, _ = io.Copy(downstream, upstream)
		}()
	}))
	if secure {
		s.StartTLS()
	} else {
		s.Start()
	}
	t.Cleanup(s.Close)
	return s, calls
}

func TestLocalTLSHTTPAndHTTPSConnectUseProfile(t *testing.T) {
	for _, secure := range []bool{false, true} {
		name := "http"
		if secure {
			name = "https"
		}
		t.Run(name, func(t *testing.T) {
			target, roots, hellos, _ := localTLSTarget(t)
			proxy, proxyCalls := localConnectProxy(t, secure)
			proxyURL, _ := url.Parse(proxy.URL)
			proxyURL.User = url.UserPassword("test", "test")
			opts := TransportOptions{RootCAs: roots}
			if secure {
				opts.ProxyRootCAs = x509.NewCertPool()
				opts.ProxyRootCAs.AddCert(proxy.Certificate())
			}
			p := BuiltinProfile("nodejs24")
			transport, err := NewHTTPTransport(p, proxyURL, opts)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(transport.CloseIdleConnections)
			localRoundTrip(t, transport, target.URL)
			assertLocalHello(t, <-hellos, p)
			if proxyCalls.Load() != 1 {
				t.Fatal("TLS request bypassed configured CONNECT proxy")
			}
		})
	}
}

func TestLocalTLSProxyErrorsNeverBypassProxy(t *testing.T) {
	target, roots, hellos, calls := localTLSTarget(t)
	for _, secure := range []bool{false, true} {
		proxy, _ := localConnectProxy(t, secure)
		proxyURL, _ := url.Parse(proxy.URL)
		// No authentication for HTTP; no trusted proxy CA for HTTPS.
		transport, err := NewHTTPTransport(BuiltinProfile("nodejs24"), proxyURL, TransportOptions{RootCAs: roots})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(transport.CloseIdleConnections)
		_, err = (&http.Client{Transport: transport, Timeout: 3 * time.Second}).Get(target.URL)
		if err == nil {
			t.Fatal("failed proxy unexpectedly allowed a request")
		}
	}
	if calls.Load() != 0 || len(hellos) != 0 {
		t.Fatal("proxy error fell back to a direct upstream connection")
	}
}

func TestLocalTLSSOCKSUsesProfile(t *testing.T) {
	target, roots, hellos, _ := localTLSTarget(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 2)
		if _, err = io.ReadFull(conn, buf); err != nil {
			return
		}
		methods := make([]byte, int(buf[1]))
		if _, err = io.ReadFull(conn, methods); err != nil {
			return
		}
		_, _ = conn.Write([]byte{5, 0})
		header := make([]byte, 4)
		if _, err = io.ReadFull(conn, header); err != nil || header[3] != 1 {
			return
		}
		addr := make([]byte, 6)
		if _, err = io.ReadFull(conn, addr); err != nil {
			return
		}
		if net.IP(addr[:4]).String() != "127.0.0.1" || binary.BigEndian.Uint16(addr[4:]) == 0 {
			return
		}
		upstream, err := net.DialTimeout("tcp", strings.TrimPrefix(target.URL, "https://"), time.Second)
		if err != nil {
			return
		}
		defer upstream.Close()
		_, _ = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
		go func() { _, _ = io.Copy(upstream, conn); _ = upstream.Close() }()
		_, _ = io.Copy(conn, upstream)
	}()
	proxyURL, _ := url.Parse("socks5://" + listener.Addr().String())
	p := BuiltinProfile("nodejs24")
	transport, err := NewHTTPTransport(p, proxyURL, TransportOptions{RootCAs: roots})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.CloseIdleConnections)
	localRoundTrip(t, transport, target.URL)
	assertLocalHello(t, <-hellos, p)
}

func TestLocalTLSConnectCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(io.Discard, conn) // accept CONNECT, never answer it
	}()
	proxyURL, _ := url.Parse("http://" + listener.Addr().String())
	transport, err := NewHTTPTransport(BuiltinProfile("nodejs24"), proxyURL, TransportOptions{HandshakeTimeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.CloseIdleConnections)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://localhost:443", nil)
	_, err = (&http.Client{Transport: transport, Timeout: time.Second}).Do(req)
	if err == nil {
		t.Fatal("stalled CONNECT did not time out")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not close proxy connection")
	}
}

func TestLocalTLSRejectsUnsupportedRoutes(t *testing.T) {
	p := BuiltinProfile("nodejs24")
	badProxy, _ := url.Parse("ftp://127.0.0.1:1")
	if _, err := NewHTTPTransport(p, badProxy, TransportOptions{}); err == nil {
		t.Fatal("unsupported proxy must fail at construction")
	}
	p.ALPNProtocols = []string{"h2", "http/1.1"}
	if _, err := NewHTTPTransport(p, nil, TransportOptions{}); err == nil {
		t.Fatal("unsupported HTTP/2 negotiation must fail at construction")
	}
	transport, err := NewHTTPTransport(BuiltinProfile("nodejs24"), nil, TransportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&http.Client{Transport: transport}).Get("http://127.0.0.1:1"); err == nil || !strings.Contains(err.Error(), "requires HTTPS") {
		t.Fatalf("plaintext route was not rejected before dialing: %v", err)
	}
}

func TestLocalTLSProfileCloneAndContentKey(t *testing.T) {
	p := BuiltinProfile("nodejs24")
	c := p.Clone()
	if c.CacheKey() != p.CacheKey() {
		t.Fatal("clone changed template key")
	}
	for name, mutate := range map[string]func(*Profile){
		"ciphers":    func(c *Profile) { c.CipherSuites[0], c.CipherSuites[1] = c.CipherSuites[1], c.CipherSuites[0] },
		"curves":     func(c *Profile) { c.Curves[0], c.Curves[1] = c.Curves[1], c.Curves[0] },
		"extensions": func(c *Profile) { c.Extensions[0], c.Extensions[1] = c.Extensions[1], c.Extensions[0] },
		"alpn":       func(c *Profile) { c.ALPNProtocols[0] = "different" },
	} {
		t.Run(name, func(t *testing.T) {
			original := p.CacheKey()
			copy := p.Clone()
			mutate(copy)
			if copy.CacheKey() == original || p.CacheKey() != original {
				t.Fatal("template edit reused key or modified original profile")
			}
		})
	}
}
