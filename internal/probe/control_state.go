package probe

import "sync"

// controlState holds host-mutating control plane state that is independent of
// observation loops: container network blocks and network-config rollbacks.
type controlState struct {
	mu        sync.RWMutex
	blocked   map[string]string // bridge -> mode
	netcfgMu  sync.Mutex
	rollbacks map[string]*networkConfigRollback
}

func newControlState() *controlState {
	return &controlState{
		blocked:   map[string]string{},
		rollbacks: map[string]*networkConfigRollback{},
	}
}

func (c *controlState) snapshotBlocked() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]string, len(c.blocked))
	for k, v := range c.blocked {
		out[k] = v
	}
	return out
}

func (c *controlState) setBlocked(bridge, mode string) {
	c.mu.Lock()
	c.blocked[bridge] = mode
	c.mu.Unlock()
}

func (c *controlState) clearBlocked(bridge string) string {
	c.mu.Lock()
	mode := c.blocked[bridge]
	delete(c.blocked, bridge)
	c.mu.Unlock()
	return mode
}

func (c *controlState) getBlocked(bridge string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.blocked[bridge]
}

func (c *controlState) replaceBlocked(in map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blocked = make(map[string]string, len(in))
	for k, v := range in {
		c.blocked[k] = v
	}
}
