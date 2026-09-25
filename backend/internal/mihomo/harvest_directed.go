package mihomo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const HarvestSelectGroup = "CODEX-HARVEST-SELECT"

// RuntimeID only fences in-flight work; Mihomo regenerates it on reload.
type HarvestNode struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	RuntimeID string `json:"-"`
}

type DirectedSidecar struct {
	Controller string
	Secret     string
	ProxyURL   string
	PoolID     string
	root       string
	config     directedConfig
	managed    *Manager
}

type directedProxy struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Provider string   `json:"provider-name"`
	Dialer   string   `json:"dialer-proxy"`
	Now      string   `json:"now"`
	All      []string `json:"all"`
}

var directedGate = make(chan struct{}, 1)
var directedHTTP = &http.Client{
	Timeout:       5 * time.Second,
	Transport:     &http.Transport{Proxy: nil, DisableKeepAlives: true},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func (s *DirectedSidecar) control(ctx context.Context, method, path string, body any, out any) error {
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, s.Controller+path, bytes.NewReader(encoded))
	if err != nil {
		return errors.New("invalid harvest controller request")
	}
	req.Header.Set("Authorization", "Bearer "+s.Secret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := directedHTTP.Do(req)
	if err != nil {
		return errors.New("harvest controller unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("harvest controller status %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out); err != nil {
		return errors.New("invalid harvest controller response")
	}
	return nil
}

func (s *DirectedSidecar) liveLeaves(ctx context.Context) (map[string]directedProxy, error) {
	var body struct {
		Providers map[string]struct {
			Name    string          `json:"name"`
			Vehicle string          `json:"vehicleType"`
			Proxies []directedProxy `json:"proxies"`
		} `json:"providers"`
	}
	if err := s.control(ctx, http.MethodGet, "/providers/proxies", nil, &body); err != nil {
		return nil, errors.New("harvest provider catalog unavailable")
	}
	out := map[string]directedProxy{}
	dup := map[string]bool{}
	for name, provider := range body.Providers {
		if provider.Vehicle == "Compatible" {
			continue
		}
		if provider.Name == "" {
			provider.Name = name
		}
		for _, p := range provider.Proxies {
			if p.Name == "" || p.ID == "" {
				continue
			}
			if p.Provider == "" {
				p.Provider = provider.Name
			}
			if prev, ok := out[p.Name]; ok && (prev.ID != p.ID || prev.Provider != p.Provider) {
				dup[p.Name] = true
				continue
			}
			out[p.Name] = p
		}
	}
	for name := range dup {
		delete(out, name)
	}
	return out, nil
}

func (s *DirectedSidecar) Directory(ctx context.Context) ([]HarvestNode, error) {
	if s.managed != nil {
		return s.managedDirectory()
	}
	var state struct {
		Proxies map[string]directedProxy `json:"proxies"`
	}
	if err := s.control(ctx, http.MethodGet, "/proxies", nil, &state); err != nil {
		return nil, err
	}
	group, ok := state.Proxies[HarvestSelectGroup]
	if !ok || group.Type != "Selector" {
		return nil, errors.New("dedicated harvest selector unavailable")
	}
	live, err := s.liveLeaves(ctx)
	if err != nil {
		return nil, err
	}
	identities, err := s.identities()
	if err != nil {
		return nil, err
	}
	nodes := make([]HarvestNode, 0, len(group.All))
	for _, name := range group.All {
		p, ok := live[name]
		node, known := identities[name]
		if !ok || !known || p.ID == "" || p.Provider != node.Provider || p.Dialer != "" || len(p.All) != 0 {
			continue
		}
		switch p.Type {
		case "Direct", "Reject", "RejectDrop", "Pass", "Compatible", "Selector", "URLTest", "Fallback", "LoadBalance":
			continue
		}
		node.RuntimeID = p.ID
		nodes = append(nodes, node)
	}
	if len(nodes) == 0 {
		return nil, errors.New("no identifiable harvest leaf nodes")
	}
	return nodes, nil
}

func (s *DirectedSidecar) Lookup(ctx context.Context, id, name string) (HarvestNode, bool) {
	nodes, err := s.Directory(ctx)
	if err != nil {
		return HarvestNode{}, false
	}
	id = strings.TrimSpace(id)
	name = strings.TrimSpace(name)
	if id != "" {
		for _, node := range nodes {
			if node.ID == id {
				return node, true
			}
		}
	}
	if name != "" {
		for _, node := range nodes {
			if node.Name == name {
				return node, true
			}
		}
	}
	return HarvestNode{}, false
}

// Acquire holds the dedicated selector until response closure and validation.
// The queue deadline is independent from the caller's upstream request timeout.
func (s *DirectedSidecar) Acquire(ctx context.Context, node HarvestNode) (func(), error) {
	if s.managed != nil {
		return s.acquireManaged(ctx, node)
	}
	wait, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	select {
	case directedGate <- struct{}{}:
	case <-wait.Done():
		return nil, wait.Err()
	}
	var once sync.Once
	release := func() { once.Do(func() { <-directedGate }) }
	if err := s.control(ctx, http.MethodPut, "/proxies/"+url.PathEscape(HarvestSelectGroup), map[string]string{"name": node.Name}, nil); err != nil {
		release()
		return nil, err
	}
	if err := s.Confirm(ctx, node); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func (s *DirectedSidecar) Confirm(ctx context.Context, node HarvestNode) error {
	if s.managed != nil {
		return s.confirmManaged(ctx, node)
	}
	var group directedProxy
	if err := s.control(ctx, http.MethodGet, "/proxies/"+url.PathEscape(HarvestSelectGroup), nil, &group); err != nil {
		return err
	}
	if group.Type != "Selector" || group.Now != node.Name {
		return errors.New("harvest selection changed")
	}
	nodes, err := s.Directory(ctx)
	if err != nil {
		return err
	}
	for _, current := range nodes {
		if current.ID == node.ID && current.RuntimeID == node.RuntimeID {
			return nil
		}
	}
	return errors.New("harvest node identity changed")
}
