package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCodex780EdgeValidation(t *testing.T) {
	for _, ip := range []string{"", "104.18.32.7", "2606:4700::1111"} {
		require.NoError(t, validateCodexMintEdgeIP(ip))
	}
	for _, ip := range []string{"localhost", "127.0.0.1", "10.1.2.3", "::ffff:127.0.0.1", "2001:db8::1", "8.8.8.8:443", "fe80::1%eth0"} {
		require.Error(t, validateCodexMintEdgeIP(ip))
	}
}
func TestCodex780EdgePreservesTLSIdentity(t *testing.T) {
	observed := make(chan string, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { observed <- r.Host; w.WriteHeader(204) }))
	sni := make(chan string, 2)
	server.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) { sni <- hello.ServerName; return nil, nil }}
	server.StartTLS()
	defer server.Close()
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	req, _ := http.NewRequestWithContext(context.Background(), "GET", "https://example.com:"+port+"/mint", nil)
	client, pinned, err := codexMintHTTPClient(req, "", "127.0.0.1")
	require.NoError(t, err)
	defer client.CloseIdleConnections()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	transport.TLSClientConfig.RootCAs = roots
	require.False(t, transport.TLSClientConfig.InsecureSkipVerify)
	response, err := client.Do(pinned)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, "example.com:"+port, <-observed)
	require.Equal(t, "example.com", <-sni)
	req.URL.Host = "wrong.example:" + port
	other, bad, err := codexMintHTTPClient(req, "", "127.0.0.1")
	require.NoError(t, err)
	defer other.CloseIdleConnections()
	otherTransport, ok := other.Transport.(*http.Transport)
	require.True(t, ok)
	otherTransport.TLSClientConfig.RootCAs = roots
	_, err = other.Do(bad)
	require.Error(t, err)
}
func TestCodex780WebSocketMetadataAndEdgeHost(t *testing.T) {
	ticket := mint780State(time.Now())
	pair := mint780Pair(time.Now().Add(time.Hour), "unified-95")
	observed := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- r.Host
		for _, cookie := range pair {
			w.Header().Add("Set-Cookie", cookie)
		}
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		_, raw, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		var payload map[string]any
		if json.Unmarshal(raw, &payload) != nil || payload["type"] != "response.create" || payload["stream"] != nil {
			return
		}
		_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"type":"response.created","response":{"id":"r1","model":"gpt-6-astra"}}`))
		metadata, _ := json.Marshal(map[string]any{"type": "codex.response.metadata", "headers": map[string]string{"x-codex-turn-state": ticket}})
		_ = conn.Write(r.Context(), coderws.MessageText, metadata)
		_, _, _ = conn.Read(r.Context())
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", strings.Replace(server.URL, "127.0.0.1", "mint.example", 1), nil)
	out := requestCodex780WS(req, "", "127.0.0.1", []byte(`{"model":"gpt-6-astra","stream":true}`), nil, "unified-95", "gpt-6-astra")
	require.NoError(t, out.Err)
	require.Equal(t, 101, out.Status)
	require.Equal(t, ticket, out.State)
	require.Equal(t, req.URL.Host, <-observed)
	require.Equal(t, "websocket", out.Transport)
}

func TestCodex780EdgeCONNECTDestination(t *testing.T) {
	target := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { target <- r.Method + " " + r.Host; w.WriteHeader(502) }))
	defer proxy.Close()
	req, _ := http.NewRequest("POST", "https://chatgpt.com/backend-api/codex/responses", nil)
	client, pinned, err := codexMintHTTPClient(req, proxy.URL, "104.18.32.7")
	require.NoError(t, err)
	defer client.CloseIdleConnections()
	_, err = client.Do(pinned)
	require.Error(t, err)
	require.EqualError(t, mintTransportError(err), "mint transport: proxy_connect_http_502")
	require.Equal(t, "CONNECT 104.18.32.7:443", <-target)
	require.Equal(t, "chatgpt.com", pinned.Host)
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	require.Equal(t, "chatgpt.com", transport.TLSClientConfig.ServerName)
}
