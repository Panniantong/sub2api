package mihomo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func directedFixture(t *testing.T) (*DirectedSidecar, func(string)) {
	t.Helper()
	var mu sync.Mutex
	now := ""
	runtimeID := "runtime-1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPut {
			var body map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			now = body["name"]
			w.WriteHeader(http.StatusNoContent)
			return
		}
		group := directedProxy{Type: "Selector", All: []string{"p-US", "DIRECT", "p-香港"}, Now: now}
		switch r.URL.Path {
		case "/providers/proxies":
			_ = json.NewEncoder(w).Encode(map[string]any{"providers": map[string]any{
				"airport": map[string]any{
					"name":        "airport",
					"vehicleType": "HTTP",
					"proxies": []directedProxy{
						{Name: "p-US", Type: "Shadowsocks", ID: runtimeID, Provider: "airport"},
						{Name: "p-香港", Type: "Shadowsocks", ID: "hk", Provider: "airport"},
					},
				},
				"default": map[string]any{
					"name":        "default",
					"vehicleType": "Compatible",
					"proxies":     []directedProxy{{Name: "DIRECT", Type: "Direct", ID: "direct"}},
				},
			}})
		case "/proxies":
			_ = json.NewEncoder(w).Encode(map[string]any{"proxies": map[string]directedProxy{
				HarvestSelectGroup: group,
				"DIRECT":           {Name: "DIRECT", Type: "Direct", ID: "direct"},
			}})
		default:
			_ = json.NewEncoder(w).Encode(group)
		}
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	root := filepath.Join(dir, "mihomo-codex")
	require.NoError(t, os.MkdirAll(root, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "provider.yaml"), []byte("proxies:\n  - {name: US, type: ss, server: example.com, port: 443, password: fixture}\n  - {name: 香港, type: ss, server: hk.example.com, port: 443}\n"), 0600))
	config := "mixed-port: 3101\nexternal-controller: " + server.URL + "\nsecret: fixture\nlisteners:\n  - {name: codex-harvest-directed, type: mixed, listen: 127.0.0.1, port: 3102, proxy: CODEX-HARVEST-SELECT}\nproxy-providers:\n  airport:\n    path: ./provider.yaml\n    override: {additional-prefix: p-}\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte(config), 0600))
	s, err := LoadDirectedSidecar(dir, "http://127.0.0.1:3101")
	require.NoError(t, err)
	return s, func(id string) { mu.Lock(); runtimeID = id; mu.Unlock() }
}

func TestDirectedIdentityPersistsButFencesKernelReload(t *testing.T) {
	s, reload := directedFixture(t)
	nodes, err := s.Directory(context.Background())
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	release, err := s.Acquire(context.Background(), nodes[0])
	require.NoError(t, err)
	defer release()
	require.NoError(t, s.Confirm(context.Background(), nodes[0]))
	reload("runtime-2")
	after, err := s.Directory(context.Background())
	require.NoError(t, err)
	require.Equal(t, nodes[0].ID, after[0].ID)
	require.Error(t, s.Confirm(context.Background(), nodes[0]))
	require.NoError(t, os.WriteFile(filepath.Join(s.root, "provider.yaml"), []byte("proxies:\n - {name: US, type: ss, server: changed.example.com, port: 443}\n"), 0600))
	changed, err := s.Directory(context.Background())
	require.NoError(t, err)
	require.NotEqual(t, nodes[0].ID, changed[0].ID)
}

func TestDirectedLeaseCancellationAndIdempotentRelease(t *testing.T) {
	s, _ := directedFixture(t)
	nodes, err := s.Directory(context.Background())
	require.NoError(t, err)
	release, err := s.Acquire(context.Background(), nodes[0])
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = s.Acquire(ctx, nodes[0])
	require.ErrorIs(t, err, context.DeadlineExceeded)
	release()
	release()
	again, err := s.Acquire(context.Background(), nodes[0])
	require.NoError(t, err)
	again()
}

func TestLoadDirectedSidecarAcceptsDirectedListener(t *testing.T) {
	s, _ := directedFixture(t)
	again, err := LoadDirectedSidecar(filepath.Dir(s.root), s.ProxyURL)
	require.NoError(t, err)
	require.Equal(t, s.ProxyURL, again.ProxyURL)
	require.Equal(t, "http://127.0.0.1:3102", again.ProxyURL)
}

func TestDirectedLookupByStableIdentity(t *testing.T) {
	s, reload := directedFixture(t)
	nodes, err := s.Directory(context.Background())
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	got, ok := s.Lookup(context.Background(), nodes[0].ID, "")
	require.True(t, ok)
	require.Equal(t, nodes[0].ID, got.ID)
	require.Equal(t, nodes[0].Name, got.Name)
	reload("runtime-2")
	after, ok := s.Lookup(context.Background(), nodes[0].ID, "")
	require.True(t, ok)
	require.Equal(t, nodes[0].ID, after.ID)
	require.NotEqual(t, nodes[0].RuntimeID, after.RuntimeID)
	_, ok = s.Lookup(context.Background(), "missing", "")
	require.False(t, ok)
}
