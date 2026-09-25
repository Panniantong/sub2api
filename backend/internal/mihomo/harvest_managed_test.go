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

func TestManagedDirectedHarvest(t *testing.T) {
	for _, useOnce := range []bool{false, true} {
		t.Run(map[bool]string{false: "rotation", true: "use_once"}[useOnce], func(t *testing.T) {
			var mu sync.Mutex
			selected := ""
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.URL.Path == "/configs" {
					w.WriteHeader(204)
					return
				}
				if r.URL.Path != "/proxies/"+collectionGroup(0) {
					w.WriteHeader(404)
					return
				}
				if r.Method == http.MethodPut {
					var body map[string]string
					_ = json.NewDecoder(r.Body).Decode(&body)
					selected = body["name"]
					w.WriteHeader(204)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"type": "Selector", "now": selected})
			}))
			defer server.Close()
			m := New(t.TempDir())
			defer m.Close()
			m.controllerURL = server.URL
			m.state.Running = true
			m.saved = saved{UseOnce: useOnce, Secret: "test", Nodes: []map[string]any{{"name": "one"}, {"name": "two"}, {"name": "blocked"}, {"name": "hk"}}, NodeNames: map[string]string{"one": "dynamic-19", "two": "dynamic-20"}, Disabled: map[string]string{"blocked": "failed"}, CountryFilter: CountryFilter{Mode: "exclude", Codes: []string{"HK"}, AllowUnknown: true}, Countries: map[string]CountryObservation{"hk": {Code: "HK"}}}
			sidecar, err := LoadDirectedSidecar("", Endpoint)
			require.NoError(t, err)
			nodes, err := sidecar.Directory(context.Background())
			require.NoError(t, err)
			require.Len(t, nodes, 2)
			require.Equal(t, "dynamic-19", nodes[0].Name)
			require.Equal(t, "managed", nodes[0].Provider)
			release, err := sidecar.Acquire(context.Background(), nodes[0])
			require.NoError(t, err)
			require.NoError(t, sidecar.Confirm(context.Background(), nodes[0]))
			require.Equal(t, collectionProxy(0), sidecar.ProxyURL)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			_, err = sidecar.Acquire(ctx, nodes[1])
			require.ErrorIs(t, err, context.DeadlineExceeded)
			if useOnce {
				data, err := os.ReadFile(filepath.Join(m.dir, "settings.json"))
				require.NoError(t, err)
				var state saved
				require.NoError(t, json.Unmarshal(data, &state))
				require.Equal(t, "used", state.Disabled["one"])
			}
			release()
			release()
			fresh, err := sidecar.Directory(context.Background())
			require.NoError(t, err)
			if useOnce {
				require.Len(t, fresh, 1)
			} else {
				require.Len(t, fresh, 2)
			}
			release, err = sidecar.Acquire(context.Background(), nodes[1])
			require.NoError(t, err)
			mu.Lock()
			selected = "one"
			mu.Unlock()
			require.ErrorContains(t, sidecar.Confirm(context.Background(), nodes[1]), "selection changed")
			release()
			if useOnce {
				_, err = sidecar.Directory(context.Background())
				require.ErrorContains(t, err, "no eligible")
				_, err = sidecar.Acquire(context.Background(), nodes[0])
				require.Error(t, err)
			}
		})
	}
}
