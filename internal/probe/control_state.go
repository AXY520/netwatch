package probe

import "sync"

// containerControlState owns container network isolation state.
type containerControlState struct {
	mu      sync.RWMutex
	blocked map[string]string // bridge -> mode
}

// networkMutationState serializes host network changes. IP, bridge and DNS
// changes share one transaction domain because applying them concurrently can
// invalidate another operation's rollback snapshot.
type networkMutationState struct {
	mu      sync.Mutex
	auditMu sync.Mutex
	active  *networkMutation

	// Typed references are compatibility views used by the existing APIs. The
	// active mutation above is the authoritative owner and permits only one host
	// network change at a time.
	rollbacks map[string]*networkConfigRollback

	// Host VM bridge create/dissolve pending rollback (at most one).
	bridgeRollback *hostBridgeRollback

	// Host DNS-only change pending rollback (at most one).
	dnsRollback *hostDNSRollback
}

func newContainerControlState() *containerControlState {
	return &containerControlState{blocked: map[string]string{}}
}

func newNetworkMutationState() *networkMutationState {
	return &networkMutationState{
		rollbacks: map[string]*networkConfigRollback{},
	}
}

func (c *containerControlState) snapshotBlocked() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]string, len(c.blocked))
	for k, v := range c.blocked {
		out[k] = v
	}
	return out
}

func (c *containerControlState) setBlocked(bridge, mode string) {
	c.mu.Lock()
	c.blocked[bridge] = mode
	c.mu.Unlock()
}

func (c *containerControlState) clearBlocked(bridge string) string {
	c.mu.Lock()
	mode := c.blocked[bridge]
	delete(c.blocked, bridge)
	c.mu.Unlock()
	return mode
}

func (c *containerControlState) getBlocked(bridge string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.blocked[bridge]
}

func (c *containerControlState) replaceBlocked(in map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blocked = make(map[string]string, len(in))
	for k, v := range in {
		c.blocked[k] = v
	}
}
