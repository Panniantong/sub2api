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

func TestCollectionIsolatesLanesAndPersistsReservations(t *testing.T) {
	var mu sync.Mutex
	selected := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/configs" {
			var payload map[string]string
			_ = json.NewDecoder(r.Body).Decode(&payload)
			mu.Lock()
			selected[r.URL.Path] = payload["name"]
			mu.Unlock()
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	m := New(t.TempDir())
	defer m.Close()
	m.controllerURL = server.URL
	m.state.Running = true
	m.saved = saved{UseOnce: true, Secret: "test", Nodes: []map[string]any{{"name": "one"}, {"name": "two"}, {"name": "disabled"}}, Disabled: map[string]string{"disabled": "disabled"}}
	c, err := BeginCollection(context.Background(), Endpoint)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	var wg sync.WaitGroup
	for lane := 0; lane < 2; lane++ {
		wg.Add(1)
		go func(lane int) { defer wg.Done(); _, _, _ = c.Next(context.Background(), lane) }(lane)
	}
	wg.Wait()
	mu.Lock()
	require.Len(t, selected, 2)
	require.NotEqual(t, selected["/proxies/CODEX-COLLECT-0"], selected["/proxies/CODEX-COLLECT-1"])
	mu.Unlock()
	data, err := os.ReadFile(filepath.Join(m.dir, "settings.json"))
	require.NoError(t, err)
	var snapshot saved
	require.NoError(t, json.Unmarshal(data, &snapshot))
	require.Equal(t, "used", snapshot.Disabled["one"])
	require.Equal(t, "used", snapshot.Disabled["two"])
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = Lease(ctx, Endpoint)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, c.Close())
	require.NoError(t, c.Close())
	proxy, release, err := PinNode(context.Background(), "one")
	require.NoError(t, err)
	require.Equal(t, collectionProxy(0), proxy)
	release()
	release()
	_, _, err = PinNode(context.Background(), "disabled")
	require.Error(t, err)
}

func TestCollectionConfigRejectsUnselectedLanesAndHonorsCountryFilter(t *testing.T) {
	m := &Manager{}
	s := saved{Nodes: []map[string]any{{"name": "allowed"}, {"name": "blocked"}, {"name": "country-blocked"}}, Disabled: map[string]string{"blocked": "disabled"}, CountryFilter: CountryFilter{Mode: "exclude", Codes: []string{"HK"}}, Countries: map[string]CountryObservation{"allowed": {Code: "US"}, "country-blocked": {Code: "HK"}}}
	data, err := m.config(s)
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(data, &cfg))
	listeners, ok := cfg["listeners"].([]any)
	require.True(t, ok)
	require.Len(t, listeners, MaxCollectLanes)
	groups, ok := cfg["proxy-groups"].([]any)
	require.True(t, ok)
	for i, l := range listeners {
		listener, ok := l.(map[string]any)
		require.True(t, ok)
		require.Equal(t, "127.0.0.1", listener["listen"])
		require.EqualValues(t, collectPort+i, listener["port"])
		group, ok := groups[i+1].(map[string]any)
		require.True(t, ok)
		require.Equal(t, listener["proxy"], group["name"])
		require.Equal(t, []any{"REJECT", "allowed"}, group["proxies"])
	}
	c, err := BeginCollection(context.Background(), "http://127.0.0.1:9999")
	require.Error(t, err)
	require.Nil(t, c)
}
