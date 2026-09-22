package mihomo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type directedConfig struct {
	Port       int    `yaml:"mixed-port"`
	Controller string `yaml:"external-controller"`
	Secret     string `yaml:"secret"`
	Listeners  []struct {
		Name   string `yaml:"name"`
		Type   string `yaml:"type"`
		Listen string `yaml:"listen"`
		Port   int    `yaml:"port"`
		Proxy  string `yaml:"proxy"`
	} `yaml:"listeners"`
	Providers map[string]struct {
		Path     string         `yaml:"path"`
		Override map[string]any `yaml:"override"`
	} `yaml:"proxy-providers"`
}

var harvestExcluded = regexp.MustCompile(`(?i)剩余|到期|官网|流量|套餐|通知|更新|过期|重置|距离下次|静态前置|住宅静态|香港|🇭🇰|Hong\s*Kong`)

func harvestDigest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// LoadDirectedSidecar accepts only the matching external loopback sidecar.
func LoadDirectedSidecar(dataDir, proxyURL string) (*DirectedSidecar, error) {
	if _, _, managed := ManagedController(); managed {
		return nil, errors.New("node learning does not override managed UseOnce")
	}
	if dataDir == "" {
		dataDir = "."
	}
	dir := filepath.Join(dataDir, "mihomo-codex")
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, errors.New("harvest sidecar config unavailable")
	}
	defer func() { _ = root.Close() }()
	raw, err := root.ReadFile("config.yaml")
	if err != nil {
		return nil, errors.New("harvest sidecar config unavailable")
	}
	var cfg directedConfig
	if yaml.Unmarshal(raw, &cfg) != nil {
		return nil, errors.New("invalid harvest sidecar config")
	}
	if !sidecarLoopbackProxy(proxyURL, cfg) {
		return nil, errors.New("harvest proxy is not the external sidecar")
	}
	controller := cfg.Controller
	if !strings.Contains(controller, "://") {
		controller = "http://" + controller
	}
	u, err := url.Parse(controller)
	if err != nil || u.User != nil || u.Scheme != "http" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		return nil, errors.New("harvest controller must be loopback HTTP")
	}
	for _, listener := range cfg.Listeners {
		if listener.Name == "codex-harvest-directed" && listener.Type == "mixed" && listener.Listen == "127.0.0.1" && listener.Port > 0 && listener.Port <= 65535 && listener.Port != cfg.Port && listener.Proxy == HarvestSelectGroup {
			return &DirectedSidecar{Controller: strings.TrimRight(controller, "/"), Secret: cfg.Secret,
				ProxyURL: fmt.Sprintf("http://127.0.0.1:%d", listener.Port), PoolID: harvestDigest([]string{controller, HarvestSelectGroup}), root: dir, config: cfg}, nil
		}
	}
	return nil, errors.New("dedicated harvest listener not configured")
}

func sidecarLoopbackProxy(proxyURL string, cfg directedConfig) bool {
	proxy, err := url.Parse(proxyURL)
	if err != nil || proxy.User != nil || proxy.Scheme != "http" || proxy.Hostname() != "127.0.0.1" {
		return false
	}
	port := proxy.Port()
	if port == strconv.Itoa(cfg.Port) {
		return true
	}
	for _, listener := range cfg.Listeners {
		if listener.Name == "codex-harvest-directed" && listener.Type == "mixed" && listener.Listen == "127.0.0.1" && listener.Port > 0 && listener.Port <= 65535 && listener.Port != cfg.Port && listener.Proxy == HarvestSelectGroup && strconv.Itoa(listener.Port) == port {
			return true
		}
	}
	return false
}

func (s *DirectedSidecar) identities() (map[string]HarvestNode, error) {
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, errors.New("harvest provider directory unavailable")
	}
	defer func() { _ = root.Close() }()
	out := map[string]HarvestNode{}
	duplicates := map[string]bool{}
	for provider, cfg := range s.config.Providers {
		raw, err := root.ReadFile(cfg.Path)
		if err != nil {
			return nil, errors.New("harvest provider snapshot unavailable")
		}
		var body struct {
			Proxies []map[string]any `yaml:"proxies"`
		}
		if yaml.Unmarshal(raw, &body) != nil {
			return nil, errors.New("invalid harvest provider snapshot")
		}
		prefix, _ := cfg.Override["additional-prefix"].(string)
		suffix, _ := cfg.Override["additional-suffix"].(string)
		for _, entry := range body.Proxies {
			name, _ := entry["name"].(string)
			kind, _ := entry["type"].(string)
			server, _ := entry["server"].(string)
			name = prefix + name + suffix
			if name == "" || len(name) > 512 || server == "" || kind == "" || harvestExcluded.MatchString(name) {
				continue
			}
			if _, exists := out[name]; exists {
				duplicates[name] = true
				continue
			}
			out[name] = HarvestNode{ID: harvestDigest([]any{provider, entry, cfg.Override}), Name: name, Provider: provider}
		}
	}
	for name := range duplicates {
		delete(out, name)
	}
	return out, nil
}
