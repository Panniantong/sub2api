package mihomo

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLegacyMigrationIsStagedAndPreservesSource(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	config := filepath.Join(source, "config.yaml")
	cache := filepath.Join(source, "airport.yaml")
	kernel := filepath.Join(source, "mihomo")
	require.NoError(t, os.WriteFile(config, []byte("proxy-providers:\n  airport:\n    url: https://example.org/sub?token=private\n"), 0600))
	require.NoError(t, os.WriteFile(cache, []byte("proxies:\n  - {name: private-node, type: http, server: localhost, port: 1234}\n"), 0600))
	require.NoError(t, os.WriteFile(kernel, []byte("#!/bin/sh\nexit 0\n"), 0700))
	require.NoError(t, PrepareLegacy(context.Background(), target, config, cache, kernel))
	b, err := os.ReadFile(filepath.Join(target, "mihomo-codex", "settings.json"))
	require.NoError(t, err)
	var got saved
	require.NoError(t, json.Unmarshal(b, &got))
	require.Len(t, got.URLs, 1)
	require.Len(t, got.Nodes, 1)
	require.NotContains(t, string(b), "private-node")
	require.NotEmpty(t, got.Secret)
	require.FileExists(t, config)
	require.FileExists(t, cache)
	require.Error(t, PrepareLegacy(context.Background(), target, config, cache, kernel))
	after, err := os.ReadFile(filepath.Join(target, "mihomo-codex", "settings.json"))
	require.NoError(t, err)
	require.Equal(t, b, after)
}

func TestLegacyMigrationInvalidCacheDoesNotClaimTarget(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "legacy.yaml")
	cache := filepath.Join(dir, "nodes.yaml")
	require.NoError(t, os.WriteFile(config, []byte("proxy-providers:\n  airport:\n    url: https://example.org/?token=secret\n"), 0600))
	require.NoError(t, os.WriteFile(cache, []byte("proxies: []\n"), 0600))
	err := PrepareLegacy(context.Background(), dir, config, cache, "missing")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret")
	require.NoDirExists(t, filepath.Join(dir, "mihomo-codex"))
}
