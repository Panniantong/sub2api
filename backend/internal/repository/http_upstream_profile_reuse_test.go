package repository

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/streamlatency"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestHTTPUpstreamAlternatingProfilesReuseNetworkConnections(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ready\"}\n\n")
	}))
	defer srv.Close()
	svc := NewHTTPUpstream(&config.Config{Gateway: config.GatewayConfig{ResponseHeaderTimeout: 600, GrokResponseHeaderTimeout: 120}}).(*httpUpstreamService)
	defer func() {
		for _, entry := range svc.clients {
			entry.client.CloseIdleConnections()
		}
	}()
	for round := 0; round < 3; round++ {
		for _, profile := range []service.HTTPUpstreamProfile{service.HTTPUpstreamProfileDefault, service.HTTPUpstreamProfileGrok} {
			ctx, _ := streamlatency.NewContext(service.WithHTTPUpstreamProfile(t.Context(), profile))
			reused := false
			ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }})
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, nil)
			require.NoError(t, err)
			resp, err := svc.Do(req, "", 1, 10)
			require.NoError(t, err)
			body, readErr := io.ReadAll(resp.Body)
			require.NoError(t, resp.Body.Close())
			require.NoError(t, readErr)
			require.Contains(t, string(body), "ready")
			require.Equal(t, round > 0, reused, "round=%d profile=%s", round, profile)
		}
	}
}

func TestHTTPUpstreamAlternatingProfilesKeepWarmClients(t *testing.T) {
	for _, fingerprint := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard", true: "fingerprint"}[fingerprint], func(t *testing.T) {
			cfg := &config.Config{Gateway: config.GatewayConfig{ResponseHeaderTimeout: 600, GrokResponseHeaderTimeout: 120}}
			svc := NewHTTPUpstream(cfg).(*httpUpstreamService)
			get := func(profile service.HTTPUpstreamProfile) *upstreamClientEntry {
				var entry *upstreamClientEntry
				var err error
				if fingerprint {
					entry, err = svc.getClientEntryWithTLS("", 1, 10, &tlsfingerprint.Profile{}, profile, false, false)
				} else {
					entry, err = svc.getClientEntry("", 1, 10, profile, false, false)
				}
				require.NoError(t, err)
				return entry
			}
			profiles := []service.HTTPUpstreamProfile{service.HTTPUpstreamProfileDefault, service.HTTPUpstreamProfileOpenAI, service.HTTPUpstreamProfileGrok}
			first := make(map[service.HTTPUpstreamProfile]*upstreamClientEntry)
			for _, p := range profiles {
				first[p] = get(p)
				defer first[p].client.CloseIdleConnections()
			}
			for i := 0; i < 3; i++ {
				for _, p := range profiles {
					require.Same(t, first[p], get(p), "alternating profiles must not evict a reusable pool")
				}
			}
			require.Equal(t, 600*time.Second, first[service.HTTPUpstreamProfileDefault].client.Transport.(*http.Transport).ResponseHeaderTimeout)
			require.Zero(t, first[service.HTTPUpstreamProfileOpenAI].client.Transport.(*http.Transport).ResponseHeaderTimeout)
			require.Equal(t, 120*time.Second, first[service.HTTPUpstreamProfileGrok].client.Transport.(*http.Transport).ResponseHeaderTimeout)
		})
	}
}
