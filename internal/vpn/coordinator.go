package vpn

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"

	"meldnet/internal/config"
)

var ErrOverlap = errors.New("network address ranges overlap")

// Coordinator serializes reservation and OS lifecycle operations across profiles.
// Failed cleanup retains its reservation even if the interface is already gone.
type Coordinator struct {
	mu     sync.Mutex
	active map[string][]netip.Prefix
}

func NewCoordinator() *Coordinator { return &Coordinator{active: map[string][]netip.Prefix{}} }

type Coordinated struct {
	Engine
	owner *Coordinator
	id    string
	pool  string
}

func (c *Coordinator) Wrap(id string, e Engine) *Coordinated {
	c.mu.Lock()
	defer c.mu.Unlock()
	if recovery, ok := e.(interface{ RecoveryPending() bool }); ok && recovery.RecoveryPending() {
		c.active[id] = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}
	}
	return &Coordinated{Engine: e, owner: c, id: id}
}
func (e *Coordinated) SetPool(pool string) {
	e.owner.mu.Lock()
	defer e.owner.mu.Unlock()
	e.pool = pool
}
func (e *Coordinated) SetRelay(n *config.Node, enabled bool) {
	if r, ok := e.Engine.(interface{ SetRelay(*config.Node, bool) }); ok {
		r.SetRelay(n, enabled)
	}
}
func (e *Coordinated) ranges(n *config.Node) []netip.Prefix {
	var out []netip.Prefix
	if p, err := netip.ParsePrefix(e.pool); err == nil {
		out = append(out, p.Masked())
	}
	if n != nil {
		for _, raw := range n.Settings.Addresses {
			if p, err := netip.ParsePrefix(raw); err == nil {
				out = append(out, p.Masked())
			}
		}
		for _, peer := range n.Settings.Peers {
			for _, raw := range peer.AllowedIPs {
				if p, err := netip.ParsePrefix(raw); err == nil {
					out = append(out, p.Masked())
				}
			}
		}
	}
	return out
}
func (c *Coordinator) conflict(id string, ranges []netip.Prefix) error {
	for other, reserved := range c.active {
		if other == id {
			continue
		}
		for _, a := range ranges {
			for _, b := range reserved {
				if a.Overlaps(b) {
					return fmt.Errorf("%w: %s (%s) conflicts with %s (%s); disconnect %s first", ErrOverlap, id, a, other, b, other)
				}
			}
		}
	}
	return nil
}
func (e *Coordinated) Warning(n *config.Node) error {
	e.owner.mu.Lock()
	defer e.owner.mu.Unlock()
	return e.owner.conflict(e.id, e.ranges(n))
}
func (e *Coordinated) Up(ctx context.Context, n *config.Node) error {
	e.owner.mu.Lock()
	defer e.owner.mu.Unlock()
	ranges := e.ranges(n)
	if err := e.owner.conflict(e.id, ranges); err != nil {
		return err
	}
	e.owner.active[e.id] = ranges
	if err := e.Engine.Up(ctx, n); err != nil {
		// Reserve until a successful cleanup, including partially failed Up.
		if cleanup := e.Engine.Down(ctx, n); cleanup != nil {
			return errors.Join(err, cleanup)
		}
		delete(e.owner.active, e.id)
		return err
	}
	return nil
}
func (e *Coordinated) Down(ctx context.Context, n *config.Node) error {
	e.owner.mu.Lock()
	defer e.owner.mu.Unlock()
	if err := e.Engine.Down(ctx, n); err != nil {
		return err
	}
	delete(e.owner.active, e.id)
	return nil
}
