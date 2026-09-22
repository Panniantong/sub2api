package mihomo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUseOnceLeasePersistsReservationAndSerializesProbes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	m := New(t.TempDir())
	defer m.Close()
	m.controllerURL = server.URL
	m.state.Installed = true
	m.state.Running = true
	m.saved = saved{UseOnce: true, Secret: "test-only", Nodes: []map[string]any{{"name": "node-one", "type": "http", "server": "127.0.0.1", "port": 1}}}
	require.NoError(t, os.WriteFile(filepath.Join(m.dir, "mihomo"), []byte("#!/bin/sh\nexit 0\n"), 0700))
	finish, err := Lease(context.Background(), Endpoint)
	require.NoError(t, err)
	b, err := os.ReadFile(filepath.Join(m.dir, "settings.json"))
	require.NoError(t, err)
	var reserved saved
	require.NoError(t, json.Unmarshal(b, &reserved))
	require.Equal(t, "used", reserved.Disabled["node-one"])
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = Lease(ctx, Endpoint)
	require.Error(t, err)
	finish(false)
	finish(false)
	require.Equal(t, "failed", m.Status().NodeStates[0].State)
	_, err = Lease(context.Background(), Endpoint)
	require.Error(t, err)
	require.NoError(t, m.run(context.Background(), "recover/node-one", m.saved))
	again, err := Lease(context.Background(), Endpoint)
	require.NoError(t, err)
	again(true)
	require.Equal(t, "used", m.Status().NodeStates[0].State)
}
