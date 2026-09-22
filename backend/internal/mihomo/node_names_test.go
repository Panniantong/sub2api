package mihomo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSubscriptionLabelsKeepNodeIdentityAndState(t *testing.T) {
	label := "🇯🇵 日本 东京 01"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"proxies": []any{
			map[string]any{"name": label, "type": "socks5", "server": "example.org", "port": 1080},
			map[string]any{"name": label, "type": "socks5", "server": "other.example.org", "port": 1080},
		}})
	}))
	defer server.Close()
	m := New(t.TempDir())
	t.Cleanup(m.Close)
	nodes, names, err := m.fetchNodes(context.Background(), []string{server.URL})
	require.NoError(t, err)
	require.Len(t, nodes, 2)
	id, ok := nodes[0]["name"].(string)
	require.True(t, ok)
	require.NotEqual(t, id, nodes[1]["name"])
	require.Equal(t, label, names[id])
	label = "🇯🇵 日本 东京 改名"
	updated, updatedNames, err := m.fetchNodes(context.Background(), []string{server.URL})
	require.NoError(t, err)
	require.Equal(t, nodes, updated, "renaming must not reset node identity")
	m.saved = saved{Nodes: updated, NodeNames: updatedNames, Disabled: map[string]string{id: "used"}}
	require.Equal(t, label, NodeDisplayName(id))
	require.Equal(t, "used", m.Status().NodeStates[0].State)
	require.Equal(t, label, m.Status().NodeStates[0].DisplayName)
	b, err := json.Marshal(m.saved)
	require.NoError(t, err)
	require.NoError(t, atomicWrite(filepath.Join(m.dir, "settings.json"), b, 0600))
	m.Close()
	restarted := New(m.dir)
	t.Cleanup(restarted.Close)
	require.Equal(t, label, NodeDisplayName(id))
	require.Equal(t, "used", restarted.Status().NodeStates[0].State)
	require.Equal(t, "node-unknown", NodeDisplayName("node-unknown"))
}

func TestNodeLabelRedactsURLAndCredentials(t *testing.T) {
	label := sanitizeNodeDisplayName("日本\nhttps://user:pass@host/sub?token=hidden token=secret password=pw")
	require.Contains(t, label, "日本")
	for _, secret := range []string{"user:pass", "hidden", "secret", "pw", "\n"} {
		require.NotContains(t, label, secret)
	}
	require.Len(t, []rune(sanitizeNodeDisplayName(strings.Repeat("日", 150))), 120)
}
