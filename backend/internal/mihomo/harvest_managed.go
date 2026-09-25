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

// Managed directed harvests share the management gate and collection listener.
// They never change the business/round-robin selector. UseOnce reservations are
// persisted before traffic and remain retired after errors or cancellation.
func (s *DirectedSidecar) managedDirectory() ([]HarvestNode, error) {
	m := s.managed
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || !m.state.Running {
		return nil, errors.New("managed Mihomo is not running")
	}
	nodes := make([]HarvestNode, 0, len(m.saved.Nodes))
	for _, entry := range m.saved.Nodes {
		id, _ := entry["name"].(string)
		if id == "" || m.saved.Disabled[id] != "" || !countryAllowed(m.saved, id) {
			continue
		}
		name := m.saved.NodeNames[id]
		if name == "" {
			name = id
		}
		nodes = append(nodes, HarvestNode{ID: id, Name: sanitizeNodeDisplayName(name), Provider: "managed", RuntimeID: harvestDigest(entry)})
	}
	if len(nodes) == 0 {
		return nil, errors.New("no eligible proxy nodes")
	}
	return nodes, nil
}

func (s *DirectedSidecar) acquireManaged(ctx context.Context, node HarvestNode) (func(), error) {
	m := s.managed
	wait, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := m.acquire(wait); err != nil {
		return nil, err
	}
	nodes, err := s.managedDirectory()
	found := false
	for _, current := range nodes {
		if current.ID == node.ID && current.RuntimeID == node.RuntimeID {
			found = true
			break
		}
	}
	if err != nil || !found {
		m.release()
		return nil, errors.New("harvest node is no longer eligible")
	}
	m.mu.Lock()
	next := m.saved
	if next.UseOnce {
		next.Disabled = copyNodeStates(next.Disabled)
		next.Disabled[node.ID] = "used"
		data, _ := json.Marshal(next)
		err = atomicWrite(filepath.Join(m.dir, "settings.json"), data, 0600)
		if err == nil {
			m.saved = next
		}
	}
	m.mu.Unlock()
	if err != nil {
		m.release()
		return nil, errors.New("cannot reserve harvest node")
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			defer m.release()
			if !next.UseOnce {
				return
			}
			finish, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			config, e := m.config(next)
			if e == nil {
				e = m.reload(finish, config, next.Secret)
			}
			if e != nil {
				m.stop()
			}
		})
	}
	payload, _ := json.Marshal(map[string]string{"name": node.ID})
	if err = m.control(ctx, http.MethodPut, "/proxies/"+collectionGroup(0), next.Secret, payload); err == nil {
		err = s.confirmManaged(ctx, node)
	}
	if err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func (s *DirectedSidecar) confirmManaged(ctx context.Context, node HarvestNode) error {
	m := s.managed
	m.mu.Lock()
	valid := !m.closed && m.state.Running && countryAllowed(m.saved, node.ID) &&
		(m.saved.Disabled[node.ID] == "" || m.saved.Disabled[node.ID] == "used")
	secret := m.saved.Secret
	controller := m.controllerURL
	found := false
	for _, entry := range m.saved.Nodes {
		if entry["name"] == node.ID && harvestDigest(entry) == node.RuntimeID {
			found = true
			break
		}
	}
	m.mu.Unlock()
	if !valid || !found {
		return errors.New("harvest node identity changed")
	}
	if controller == "" {
		controller = "http://127.0.0.1:9098"
	}
	client := &DirectedSidecar{Controller: controller, Secret: secret}
	var group directedProxy
	if err := client.control(ctx, http.MethodGet, "/proxies/"+collectionGroup(0), nil, &group); err != nil {
		return err
	}
	if group.Type != "Selector" || group.Now != node.ID {
		return errors.New("harvest selection changed")
	}
	return nil
}
