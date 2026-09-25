package service

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Edge selection changes the dial destination only. Host, SNI and certificate
// verification continue to use the original upstream hostname, including over CONNECT.
func codexMintHTTPClient(req *http.Request, proxy, edge string) (*http.Client, *http.Request, error) {
	clone := req.Clone(req.Context())
	origin := req.URL.Hostname()
	clone.Host = req.URL.Host
	if edge != "" {
		port := req.URL.Port()
		if port == "" {
			if req.URL.Scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
		clone.URL.Host = net.JoinHostPort(edge, port)
	}
	transport := &http.Transport{DisableKeepAlives: true, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 60 * time.Second,
		DialContext:     (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSClientConfig: &tls.Config{ServerName: origin, MinVersion: tls.VersionTLS12},
	}
	if proxy != "" {
		parsed, err := url.Parse(proxy)
		if err != nil || parsed.Host == "" {
			return nil, nil, errors.New("invalid mint proxy")
		}
		transport.Proxy = http.ProxyURL(parsed)
		transport.OnProxyConnectResponse = func(_ context.Context, _ *url.URL, _ *http.Request, resp *http.Response) error {
			if resp.StatusCode != http.StatusOK {
				return &codexMintError{kind: "network_error", detail: fmt.Sprintf("mint transport: proxy_connect_http_%d", resp.StatusCode)}
			}
			return nil
		}
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return client, clone, nil
}

var codexMintNonPublic = func() []netip.Prefix {
	ranges := strings.Fields("0.0.0.0/8 10.0.0.0/8 100.64.0.0/10 127.0.0.0/8 169.254.0.0/16 172.16.0.0/12 192.0.0.0/24 192.0.2.0/24 192.168.0.0/16 198.18.0.0/15 198.51.100.0/24 203.0.113.0/24 224.0.0.0/3 ::/128 ::1/128 64:ff9b::/96 100::/64 2001:db8::/32 fc00::/7 fe80::/10 ff00::/8")
	out := make([]netip.Prefix, 0, len(ranges))
	for _, r := range ranges {
		out = append(out, netip.MustParsePrefix(r))
	}
	return out
}()

func validateCodexMintEdgeIP(raw string) error {
	if raw == "" {
		return nil
	}
	ip, err := netip.ParseAddr(raw)
	if err != nil || ip.Zone() != "" {
		return errors.New("edge_ip must be a public IP literal")
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() {
		return errors.New("edge_ip must be a public IP literal")
	}
	for _, prefix := range codexMintNonPublic {
		if prefix.Contains(ip) {
			return errors.New("edge_ip must be a public IP literal")
		}
	}
	return nil
}

type codexMintEdgeContextKey struct{}

// WithCodexHarvestEdgeIP validates the admin mint endpoint's per-request edge override.
// The control header is consumed locally and is never forwarded upstream.
func WithCodexHarvestEdgeIP(ctx context.Context, raw string) (context.Context, error) {
	edge := strings.TrimSpace(raw)
	if edge == "" {
		return ctx, nil
	}
	if err := validateCodexMintEdgeIP(edge); err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, codexMintEdgeContextKey{}, edge), nil
}
