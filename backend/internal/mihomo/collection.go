package mihomo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"
)

const MaxCollectLanes = 32
const collectPort = 17893

func collectionGroup(lane int) string { return fmt.Sprintf("CODEX-COLLECT-%d", lane) }
func collectionProxy(lane int) string { return fmt.Sprintf("http://127.0.0.1:%d", collectPort+lane) }

// Collection owns the management gate for the whole round. Each worker owns
// one selector/listener; management and use-once probes cannot change its exit.
type Collection struct {
	m     *Manager
	mu    sync.Mutex
	nodes []string
	next  int
	once  sync.Once
}

func managedManager() *Manager {
	var selected *Manager
	managers.Range(func(key, _ any) bool {
		m, ok := key.(*Manager)
		if !ok {
			return true
		}
		m.mu.Lock()
		ready := !m.closed && m.state.Running
		m.mu.Unlock()
		if ready {
			selected = m
			return false
		}
		return true
	})
	return selected
}

func BeginCollection(ctx context.Context, proxy string) (*Collection, error) {
	if proxy != Endpoint {
		return nil, errors.New("parallel collection requires the managed Mihomo proxy")
	}
	m := managedManager()
	if m == nil {
		return nil, errors.New("managed Mihomo is not running")
	}
	if err := m.acquire(ctx); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || !m.state.Running {
		m.release()
		return nil, errors.New("managed Mihomo is not running")
	}
	c := &Collection{m: m}
	for _, n := range m.saved.Nodes {
		name, _ := n["name"].(string)
		if name != "" && m.saved.Disabled[name] == "" && countryAllowed(m.saved, name) {
			c.nodes = append(c.nodes, name)
		}
	}
	if len(c.nodes) == 0 {
		m.release()
		return nil, errors.New("no eligible proxy nodes")
	}
	return c, nil
}

// Next persists use-once reservations before exposing an exit to a worker.
func (c *Collection) Next(ctx context.Context, lane int) (node, proxy string, err error) {
	if lane < 0 || lane >= MaxCollectLanes {
		return "", "", errors.New("invalid collection lane")
	}
	if err = ctx.Err(); err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.next >= len(c.nodes) {
		return "", "", nil
	}
	node = c.nodes[c.next]
	c.next++
	c.m.mu.Lock()
	next := c.m.saved
	if next.UseOnce {
		next.Disabled = copyNodeStates(next.Disabled)
		next.Disabled[node] = "used"
		data, _ := json.Marshal(next)
		if err = atomicWrite(filepath.Join(c.m.dir, "settings.json"), data, 0600); err == nil {
			c.m.saved = next
		}
	}
	c.m.mu.Unlock()
	if err != nil {
		return "", "", errors.New("cannot reserve collection node")
	}
	payload, _ := json.Marshal(map[string]string{"name": node})
	if err = c.m.control(ctx, http.MethodPut, "/proxies/"+collectionGroup(lane), next.Secret, payload); err != nil {
		return "", "", err
	}
	return node, collectionProxy(lane), nil
}

func copyNodeStates(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (c *Collection) Close() error {
	var result error
	c.once.Do(func() {
		defer c.m.release()
		c.m.mu.Lock()
		next := c.m.saved
		c.m.mu.Unlock()
		if !next.UseOnce {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		config, err := c.m.config(next)
		if err == nil {
			err = c.m.reload(ctx, config, next.Secret)
		}
		if err != nil {
			c.m.stop()
			result = errors.New("cannot apply reserved nodes; managed proxy stopped")
		}
	})
	return result
}

// PinNode uses a dedicated lane, even when the node was retired from harvesting.
// Explicitly disabled/failed nodes and country exclusions remain enforced.
func PinNode(ctx context.Context, name string) (string, func(), error) {
	noop := func() {}
	m := managedManager()
	if m == nil {
		return "", noop, errors.New("managed Mihomo is not running")
	}
	if err := m.acquire(ctx); err != nil {
		return "", noop, err
	}
	m.mu.Lock()
	snapshot := m.saved
	closed := m.closed
	running := m.state.Running
	m.mu.Unlock()
	allowed := false
	for _, n := range snapshot.Nodes {
		if n["name"] == name && countryAllowed(snapshot, name) && (snapshot.Disabled[name] == "" || snapshot.Disabled[name] == "used") {
			allowed = true
		}
	}
	if !allowed || closed || !running {
		m.release()
		return "", noop, errors.New("ticket exit is unavailable")
	}
	payload, _ := json.Marshal(map[string]string{"name": name})
	if err := m.control(ctx, http.MethodPut, "/proxies/"+collectionGroup(0), snapshot.Secret, payload); err != nil {
		m.release()
		return "", noop, err
	}
	var once sync.Once
	return collectionProxy(0), func() { once.Do(m.release) }, nil
}
