package tlsfingerprint

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// TransportOptions configures transport plumbing, not the account fingerprint.
// Nil certificate pools use the system trust store. Certificate verification
// cannot be disabled; local tests may supply a test CA without weakening TLS.
type TransportOptions struct {
	DialContext      func(context.Context, string, string) (net.Conn, error)
	RootCAs          *x509.CertPool
	ProxyRootCAs     *x509.CertPool
	HandshakeTimeout time.Duration
}

// NewHTTPTransport returns a native HTTP/1.1 transport that also supports a
// WebSocket 101 upgrade. No response-body wrapper is used. Plain HTTP and
// unsupported proxy/protocol combinations fail instead of bypassing TLS.
func NewHTTPTransport(profile *Profile, proxyURL *url.URL, opts TransportOptions) (*http.Transport, error) {
	t := &http.Transport{MaxIdleConns: 100, MaxIdleConnsPerHost: 10, IdleConnTimeout: 90 * time.Second}
	if err := ConfigureTransport(t, profile, proxyURL, opts); err != nil {
		return nil, err
	}
	return t, nil
}

// ConfigureTransport installs the same verified uTLS path for HTTP and WSS.
// Pool sizing and response timeouts already set on t are retained.
func ConfigureTransport(t *http.Transport, profile *Profile, proxyURL *url.URL, opts TransportOptions) error {
	if t == nil || profile == nil {
		return fmt.Errorf("TLS fingerprint transport requires a transport and profile")
	}
	for _, protocol := range profile.ALPNProtocols {
		if protocol != "http/1.1" {
			return fmt.Errorf("TLS fingerprint transport does not support ALPN %q", protocol)
		}
	}
	dial, err := configuredTLSDialer(profile, proxyURL, opts)
	if err != nil {
		return err
	}
	// net/http cannot infer negotiated HTTP/2 from a *utls.UConn. Advertising
	// h2 and then writing HTTP/1.1 would corrupt the connection.
	t.ForceAttemptHTTP2 = false
	t.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
	t.Proxy = nil // CONNECT is performed by the dialer, including HTTPS proxies.
	t.DialTLS = nil
	t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}
	t.DialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, fmt.Errorf("TLS fingerprint transport requires HTTPS or WSS")
	}
	t.DialTLSContext = dial
	return nil
}

func configuredTLSDialer(profile *Profile, proxyURL *url.URL, opts TransportOptions) (func(context.Context, string, string) (net.Conn, error), error) {
	p := profile.Clone()
	if opts.HandshakeTimeout <= 0 {
		opts.HandshakeTimeout = 10 * time.Second
	}
	if opts.DialContext == nil {
		opts.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	if opts.RootCAs != nil {
		opts.RootCAs = opts.RootCAs.Clone()
	}
	if opts.ProxyRootCAs != nil {
		opts.ProxyRootCAs = opts.ProxyRootCAs.Clone()
	}
	var proxyCopy *url.URL
	if proxyURL != nil {
		purl := *proxyURL
		purl.Scheme = strings.ToLower(purl.Scheme)
		if purl.Hostname() == "" {
			return nil, fmt.Errorf("TLS fingerprint proxy host is missing")
		}
		switch purl.Scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return nil, fmt.Errorf("unsupported TLS fingerprint proxy scheme %q", purl.Scheme)
		}
		proxyCopy = &purl
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, opts.HandshakeTimeout)
		defer cancel()
		conn, err := openTunnel(ctx, network, addr, proxyCopy, opts)
		if err != nil {
			return nil, err
		}
		return performTLSHandshakeWithRoots(ctx, conn, p, addr, opts.RootCAs)
	}, nil
}

type contextProxyDialer struct {
	ctx  context.Context
	dial func(context.Context, string, string) (net.Conn, error)
}

func (d contextProxyDialer) Dial(network, addr string) (net.Conn, error) {
	return d.dial(d.ctx, network, addr)
}

func (d contextProxyDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return d.dial(ctx, network, addr)
}

func openTunnel(ctx context.Context, network, addr string, proxyURL *url.URL, opts TransportOptions) (net.Conn, error) {
	if proxyURL == nil {
		return opts.DialContext(ctx, network, addr)
	}
	proxyAddr := proxyURL.Host
	if proxyURL.Port() == "" {
		port := "1080"
		if proxyURL.Scheme == "http" {
			port = "80"
		} else if proxyURL.Scheme == "https" {
			port = "443"
		}
		proxyAddr = net.JoinHostPort(proxyURL.Hostname(), port)
	}
	if proxyURL.Scheme == "socks5" || proxyURL.Scheme == "socks5h" {
		var auth *proxy.Auth
		if proxyURL.User != nil {
			password, _ := proxyURL.User.Password()
			auth = &proxy.Auth{User: proxyURL.User.Username(), Password: password}
		}
		dialer, err := proxy.SOCKS5(network, proxyAddr, auth, contextProxyDialer{ctx: ctx, dial: opts.DialContext})
		if err != nil {
			return nil, fmt.Errorf("create SOCKS proxy: %w", err)
		}
		contextDialer, ok := dialer.(proxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("SOCKS proxy does not support context cancellation")
		}
		return contextDialer.DialContext(ctx, network, addr)
	}
	conn, err := opts.DialContext(ctx, network, proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("connect to proxy: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = conn.Close()
		}
	}()
	// CONNECT reads/writes must obey cancellation even when no deadline was
	// supplied by the request. configuredTLSDialer always supplies a timeout.
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stop()
	if proxyURL.Scheme == "https" {
		outer := tls.Client(conn, &tls.Config{
			ServerName: proxyURL.Hostname(), RootCAs: opts.ProxyRootCAs,
			MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"},
		})
		if err := outer.HandshakeContext(ctx); err != nil {
			return nil, fmt.Errorf("HTTPS proxy TLS handshake: %w", err)
		}
		conn = outer
	}
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: addr}, Host: addr, Header: make(http.Header)}
	if proxyURL.User != nil {
		password, _ := proxyURL.User.Password()
		value := base64.StdEncoding.EncodeToString([]byte(proxyURL.User.Username() + ":" + password))
		req.Header.Set("Proxy-Authorization", "Basic "+value)
	}
	if err := req.Write(conn); err != nil {
		return nil, fmt.Errorf("write proxy CONNECT: %w", err)
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		return nil, fmt.Errorf("read proxy CONNECT: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("proxy CONNECT returned HTTP %d", resp.StatusCode)
	}
	// A successful CONNECT transfers ownership of the socket to the tunnel.
	// Closing its response body would close the tunnel before TLS starts.
	if reader.Buffered() > 0 {
		conn = &bufferedTunnelConn{Conn: conn, reader: reader}
	}
	complete = true
	return conn, nil
}

type bufferedTunnelConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedTunnelConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
