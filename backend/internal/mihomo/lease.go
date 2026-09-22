package mihomo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"time"
)

// Lease serializes harvests only when use-once is enabled. The selector stays
// fixed until the response body is closed, so concurrent accounts cannot swap
// a connection's exit while the CONNECT is being established.
func Lease(ctx context.Context, proxy string) (func(bool), error) {
	noop := func(bool) {}
	if proxy != Endpoint {
		return noop, nil
	}
	var selected *Manager
	managers.Range(func(key, value any) bool {
		m, ok := key.(*Manager)
		if !ok {
			return true
		}
		m.mu.Lock()
		enabled := m.saved.UseOnce && !m.closed
		m.mu.Unlock()
		if enabled {
			selected = m
			return false
		}
		return true
	})
	if selected == nil {
		return noop, nil
	}
	m := selected
	if err := m.acquire(ctx); err != nil {
		return nil, err
	}
	m.mu.Lock()
	snapshot := m.saved
	running := m.state.Running
	closed := m.closed
	m.mu.Unlock()
	if !snapshot.UseOnce {
		m.release()
		return noop, nil
	}
	if !running || closed {
		m.release()
		return nil, errors.New("managed proxy is not ready")
	}
	name := ""
	for _, n := range snapshot.Nodes {
		candidate, _ := n["name"].(string)
		if candidate != "" && snapshot.Disabled[candidate] == "" && countryAllowed(snapshot, candidate) {
			name = candidate
			break
		}
	}
	if name == "" {
		m.release()
		return nil, errors.New("no eligible proxy nodes; check country rules or manually recover nodes")
	}
	retired := map[string]string{}
	for key, value := range snapshot.Disabled {
		retired[key] = value
	}
	retired[name] = "used"
	snapshot.Disabled = retired
	stored, _ := json.Marshal(snapshot)
	if err := atomicWrite(filepath.Join(m.dir, "settings.json"), stored, 0600); err != nil {
		m.release()
		return nil, errors.New("cannot reserve proxy node")
	}
	m.mu.Lock()
	m.saved = snapshot
	m.mu.Unlock()
	payload, _ := json.Marshal(map[string]string{"name": name})
	if err := m.control(ctx, http.MethodPut, "/proxies/CODEX-ROTATE", snapshot.Secret, payload); err != nil {
		m.stop()
		m.release()
		return nil, err
	}
	var once sync.Once
	return func(success bool) {
		once.Do(func() {
			defer m.release()
			// Finishing a cancelled probe must still retire its exit. Never reset an
			// account's ticket cooldown here, including on manual node recovery.
			finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			action := "used/" + name
			if !success {
				action = "failed/" + name
			}
			if err := m.run(finishCtx, action, snapshot); err != nil {
				m.mu.Lock()
				m.state.Error = "cannot persist retired node; kernel stopped"
				m.mu.Unlock()
				m.stop()
			}
		})
	}, nil
}

func (m *Manager) acquire(ctx context.Context) error {
	select {
	case m.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *Manager) release() { <-m.gate }
