package vpn

import (
	"context"
	"errors"
	"sync"
	"testing"

	"meldnet/internal/config"
)

func rangeNode(t *testing.T, address, pool string) *config.Node {
	t.Helper()
	peer, err := config.New(config.Settings{Name: "peer", Addresses: []string{"10.1.0.1/32"}})
	if err != nil {
		t.Fatal(err)
	}
	node, err := config.New(config.Settings{Name: "test", Addresses: []string{address}, Peers: []config.Peer{{Name: "peer", PublicKey: peer.Public().PublicKey, AllowedIPs: []string{pool}}}})
	if err != nil {
		t.Fatal(err)
	}
	return node
}
func TestConcurrentRangeReservation(t *testing.T) {
	for _, pool := range []string{"10.77.0.0/24", "10.77.0.128/25", "10.77.0.0/16"} {
		t.Run(pool, func(t *testing.T) {
			c := NewCoordinator()
			a := c.Wrap("a", &Simulator{})
			b := c.Wrap("b", &Simulator{})
			na := rangeNode(t, "10.77.0.2/32", "10.77.0.0/24")
			nb := rangeNode(t, "10.77.0.130/32", pool)
			start := make(chan struct{})
			results := make(chan error, 2)
			var wg sync.WaitGroup
			for _, p := range []struct {
				e *Coordinated
				n *config.Node
			}{{a, na}, {b, nb}} {
				wg.Add(1)
				go func() { defer wg.Done(); <-start; results <- p.e.Up(context.Background(), p.n) }()
			}
			close(start)
			wg.Wait()
			close(results)
			successes, conflicts := 0, 0
			for err := range results {
				if err == nil {
					successes++
				} else if errors.Is(err, ErrOverlap) {
					conflicts++
				} else {
					t.Fatal(err)
				}
			}
			if successes != 1 || conflicts != 1 {
				t.Fatalf("successes %d conflicts %d", successes, conflicts)
			}
		})
	}
}

type cleanupFailure struct {
	Simulator
	fail bool
}

func (e *cleanupFailure) Down(ctx context.Context, n *config.Node) error {
	_ = e.Simulator.Down(ctx, n)
	if e.fail {
		return errors.New("cleanup failed")
	}
	return nil
}
func TestCleanupReservesRangesAndIndependentDown(t *testing.T) {
	ctx := context.Background()
	c := NewCoordinator()
	bad := &cleanupFailure{fail: true}
	a := c.Wrap("a", bad)
	b := c.Wrap("b", &Simulator{})
	other := c.Wrap("other", &Simulator{})
	na := rangeNode(t, "10.77.0.2/32", "10.77.0.0/24")
	nb := rangeNode(t, "10.88.0.2/32", "10.88.0.0/24")
	if err := a.Up(ctx, na); err != nil {
		t.Fatal(err)
	}
	if err := other.Up(ctx, nb); err != nil {
		t.Fatal(err)
	}
	if a.Down(ctx, na) == nil {
		t.Fatal("cleanup error lost")
	}
	if !errors.Is(b.Up(ctx, na), ErrOverlap) {
		t.Fatal("failed cleanup released reservation")
	}
	state, _ := other.Status(ctx, nb)
	if !state.Up {
		t.Fatal("unrelated network stopped")
	}
	bad.fail = false
	if err := a.Down(ctx, na); err != nil {
		t.Fatal(err)
	}
	if err := b.Up(ctx, na); err != nil {
		t.Fatal(err)
	}
}

func TestIPv6Overlap(t *testing.T) {
	c := NewCoordinator()
	a, b := c.Wrap("a", &Simulator{}), c.Wrap("b", &Simulator{})
	if err := a.Up(context.Background(), rangeNode(t, "fd77::2/128", "fd77::/64")); err != nil {
		t.Fatal(err)
	}
	if err := b.Up(context.Background(), rangeNode(t, "fd77::3/128", "fd77::/80")); !errors.Is(err, ErrOverlap) {
		t.Fatalf("nested IPv6 range accepted: %v", err)
	}
	if err := b.Up(context.Background(), rangeNode(t, "fd88::2/128", "fd88::/64")); err != nil {
		t.Fatal(err)
	}
}
