package mihomo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// PrepareLegacy stages data only; stopping the old service and transferring
// directory ownership belong to the privileged deployment script.
func PrepareLegacy(ctx context.Context, dataDir, configPath, cachePath, binaryPath string) error {
	target := filepath.Join(dataDir, "mihomo-codex")
	if _, err := os.Stat(target); err == nil {
		return errors.New("managed directory already exists; keep it and migrate explicitly")
	}
	configBytes, err := readBoundedFile(configPath, 4<<20)
	if err != nil {
		return errors.New("cannot read legacy configuration")
	}
	var legacy struct {
		Providers map[string]struct {
			URL string `yaml:"url"`
		} `yaml:"proxy-providers"`
	}
	if yaml.Unmarshal(configBytes, &legacy) != nil || len(legacy.Providers) != 1 {
		return errors.New("expected the legacy single airport provider")
	}
	provider, ok := legacy.Providers["airport"]
	if !ok {
		return errors.New("legacy airport provider missing")
	}
	urls, err := normalizeURLs([]string{provider.URL})
	if err != nil || len(urls) != 1 {
		return errors.New("invalid legacy subscription")
	}
	cached, err := readBoundedFile(cachePath, 4<<20)
	if err != nil {
		return errors.New("cannot read legacy node cache")
	}
	var nodes struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if yaml.Unmarshal(cached, &nodes) != nil || len(nodes.Proxies) == 0 || len(nodes.Proxies) > 1000 {
		return errors.New("invalid legacy node cache")
	}
	names := map[string]bool{}
	for _, node := range nodes.Proxies {
		name, ok := node["name"].(string)
		if !ok || name == "" || names[name] {
			return errors.New("invalid legacy node names")
		}
		names[name] = true
	}
	// Node display names may contain provider-specific private information.
	for i, node := range nodes.Proxies {
		delete(node, "dialer-proxy")
		node["name"] = fmt.Sprintf("migrated-node-%d", i+1)
	}
	binary, err := readBoundedFile(binaryPath, 192<<20)
	if err != nil {
		return errors.New("cannot read legacy kernel")
	}
	stage, err := os.MkdirTemp("", ".mihomo-migration-*")
	if err != nil {
		return errors.New("cannot stage kernel migration")
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err = atomicWrite(filepath.Join(stage, "mihomo"), binary, 0700); err != nil {
		return err
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return errors.New("cannot generate secret")
	}
	snapshot := saved{URLs: urls, Nodes: nodes.Proxies, Secret: hex.EncodeToString(secret)}
	manager := &Manager{dir: stage, client: &http.Client{Timeout: 30 * time.Second}}
	payload, err := manager.config(snapshot)
	if err != nil {
		return err
	}
	path := filepath.Join(stage, "candidate.json")
	if err = atomicWrite(path, payload, 0600); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, filepath.Join(stage, "mihomo"), "-d", stage, "-f", path, "-t")
	if cmd.Run() != nil {
		return errors.New("legacy kernel rejected migrated configuration")
	}
	b, _ := json.Marshal(snapshot)
	if err = atomicWrite(filepath.Join(stage, "settings.json"), b, 0600); err != nil {
		return err
	}
	if err = os.Rename(stage, target); err != nil {
		return errors.New("cannot activate migration directory")
	}
	return nil
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, errors.New("file incomplete or too large")
	}
	return b, nil
}

func CheckManaged(ctx context.Context, dataDir string) error {
	b, err := readBoundedFile(filepath.Join(dataDir, "mihomo-codex", "settings.json"), 8<<20)
	if err != nil {
		return errors.New("managed settings unavailable")
	}
	var s saved
	if json.Unmarshal(b, &s) != nil || s.Secret == "" {
		return errors.New("managed settings invalid")
	}
	m := &Manager{}
	if err = m.control(ctx, http.MethodGet, "/version", s.Secret, nil); err != nil {
		return err
	}
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", "127.0.0.1:3101")
	if err != nil {
		return errors.New("managed proxy unavailable")
	}
	return conn.Close()
}
