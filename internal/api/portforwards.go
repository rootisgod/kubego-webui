package api

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// PortForwardEntry is the wire shape for an active user-facing port-forward.
// Forwards are server-local state — they don't survive a restart and they
// don't follow the user across cluster switches.
type PortForwardEntry struct {
	ID         string    `json:"id"`
	VM         string    `json:"vm"`
	RemotePort int       `json:"remote_port"`
	LocalPort  int       `json:"local_port"`
	Protocol   string    `json:"protocol"`        // "ssh" | "http" | "tcp" (hint for UI, not enforced)
	Label      string    `json:"label,omitempty"` // user-friendly name
	Created    time.Time `json:"created"`
	LastUsed   time.Time `json:"last_used"`
	Context    string    `json:"context"` // kube context the forward was opened against
}

type portForwardInstance struct {
	entry PortForwardEntry
	stop  func()
}

// portForwardManager owns all active port-forwards created via the Connect
// panel and the HTTP reverse proxy. One entry per (context, vm, remotePort).
// Forwards time out after idleAfter of no activity and on cluster switch.
type portForwardManager struct {
	server    *Server
	idleAfter time.Duration

	mu     sync.Mutex
	byKey  map[string]*portForwardInstance
	byID   map[string]*portForwardInstance
	stopCh chan struct{}
}

func newPortForwardManager(server *Server) *portForwardManager {
	m := &portForwardManager{
		server:    server,
		idleAfter: 30 * time.Minute,
		byKey:     make(map[string]*portForwardInstance),
		byID:      make(map[string]*portForwardInstance),
		stopCh:    make(chan struct{}),
	}
	go m.sweepLoop()
	return m
}

func pfKey(ctx, vm string, port int) string {
	return fmt.Sprintf("%s|%s|%d", ctx, vm, port)
}

// EnsureOpen returns the active forward for (vm, remotePort) on the current
// cluster, opening one if none exists. Always refreshes LastUsed. Safe for
// concurrent callers — if two goroutines race to open the same forward, one
// loses and closes its excess tunnel.
func (m *portForwardManager) EnsureOpen(ctx context.Context, vm string, remotePort int, protocol, label string) (PortForwardEntry, error) {
	kubeContext := m.server.clusters.ActiveContext()
	key := pfKey(kubeContext, vm, remotePort)

	m.mu.Lock()
	if existing, ok := m.byKey[key]; ok {
		existing.entry.LastUsed = time.Now()
		if label != "" {
			existing.entry.Label = label
		}
		if protocol != "" {
			existing.entry.Protocol = protocol
		}
		entry := existing.entry
		m.mu.Unlock()
		return entry, nil
	}
	m.mu.Unlock()

	// StartPortForward blocks on the apiserver handshake — do it outside
	// the lock so we don't serialise forward creation across VMs.
	localPort, stop, err := m.server.kv().StartPortForward(ctx, vm, remotePort)
	if err != nil {
		return PortForwardEntry{}, err
	}

	id := fmt.Sprintf("pf-%s-%d-%d", vm, remotePort, time.Now().UnixNano())
	entry := PortForwardEntry{
		ID:         id,
		VM:         vm,
		RemotePort: remotePort,
		LocalPort:  localPort,
		Protocol:   protocol,
		Label:      label,
		Created:    time.Now(),
		LastUsed:   time.Now(),
		Context:    kubeContext,
	}
	inst := &portForwardInstance{entry: entry, stop: stop}

	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.byKey[key]; ok {
		// Lost the race — discard ours and return the winner.
		stop()
		existing.entry.LastUsed = time.Now()
		return existing.entry, nil
	}
	m.byKey[key] = inst
	m.byID[id] = inst
	return entry, nil
}

func (m *portForwardManager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.byID[id]
	if !ok {
		return fmt.Errorf("port-forward not found")
	}
	inst.stop()
	delete(m.byID, id)
	delete(m.byKey, pfKey(inst.entry.Context, inst.entry.VM, inst.entry.RemotePort))
	return nil
}

// ListForVM returns active forwards for a VM in the currently-active cluster.
// Forwards from other contexts are filtered out — they may still be alive,
// but the UI only speaks to the current cluster.
func (m *portForwardManager) ListForVM(vm string) []PortForwardEntry {
	active := m.server.clusters.ActiveContext()
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PortForwardEntry, 0)
	for _, inst := range m.byID {
		if inst.entry.VM == vm && inst.entry.Context == active {
			out = append(out, inst.entry)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

// touchByKey bumps LastUsed so the proxy route can signal "still in use"
// without fetching the whole entry.
func (m *portForwardManager) touchByKey(ctx, vm string, port int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.byKey[pfKey(ctx, vm, port)]; ok {
		inst.entry.LastUsed = time.Now()
	}
}

// DropAll closes every forward. Called on cluster switch and shutdown.
func (m *portForwardManager) DropAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, inst := range m.byID {
		inst.stop()
	}
	m.byID = make(map[string]*portForwardInstance)
	m.byKey = make(map[string]*portForwardInstance)
}

func (m *portForwardManager) Shutdown() {
	select {
	case <-m.stopCh:
		// already closed
	default:
		close(m.stopCh)
	}
	m.DropAll()
}

func (m *portForwardManager) sweepLoop() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-t.C:
			m.sweep()
		}
	}
}

func (m *portForwardManager) sweep() {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, inst := range m.byID {
		if now.Sub(inst.entry.LastUsed) > m.idleAfter {
			inst.stop()
			delete(m.byID, id)
			delete(m.byKey, pfKey(inst.entry.Context, inst.entry.VM, inst.entry.RemotePort))
			m.server.logger.Info("port-forward idle-reaped", "id", inst.entry.ID, "vm", inst.entry.VM, "remote_port", inst.entry.RemotePort)
		}
	}
}

