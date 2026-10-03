package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"meldnet/internal/config"
	"meldnet/internal/vpn"
)

type memoryStore struct {
	n    *config.Node
	fail bool
}

func (m *memoryStore) Load() (*config.Node, error) { return m.n, nil }
func (m *memoryStore) Save(n *config.Node) error {
	if m.fail {
		return errors.New("disk full")
	}
	copy := *n
	m.n = &copy
	return nil
}
func testSettings() config.Settings {
	return config.Settings{Name: "node", Addresses: []string{"10.77.0.1/24"}, ListenPort: 51820, Peers: []config.Peer{}}
}

func TestLifecycleAndIdentity(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{}
	engine := &vpn.Simulator{}
	s, _ := New(store, engine)
	if err := s.Connect(ctx); !errors.Is(err, ErrUninitialized) {
		t.Fatal(err)
	}
	p, err := s.Configure(ctx, testSettings(), 0)
	if err != nil {
		t.Fatal(err)
	}
	key := p.PublicKey
	if err := s.Connect(ctx); err == nil {
		t.Fatal("connected without peers")
	}
	peer, _ := config.New(testSettings())
	settings := p.Settings
	settings.Peers = []config.Peer{{Name: "peer", PublicKey: peer.Public().PublicKey, AllowedIPs: []string{"10.77.0.2/32"}}}
	p, err = s.Configure(ctx, settings, p.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if p.PublicKey != key {
		t.Fatal("configuration rotated identity")
	}
	if _, err := s.Configure(ctx, settings, p.Revision-1); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err := s.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Configure(ctx, settings, p.Revision); !errors.Is(err, ErrRunning) {
		t.Fatal(err)
	}
	if err := s.Connect(ctx); err != nil {
		t.Fatal("idempotent connect:", err)
	}
	// A fresh service adopts the engine's observed state without a frontend.
	restarted, _ := New(store, engine)
	if !restarted.Status(ctx).Tunnel.Up {
		t.Fatal("lost tunnel state")
	}
	if err := restarted.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Disconnect(ctx); err != nil {
		t.Fatal("idempotent down:", err)
	}
	status := restarted.Status(ctx)
	status.Node.Settings.Addresses[0] = "bad"
	if restarted.Status(ctx).Node.Settings.Addresses[0] == "bad" {
		t.Fatal("caller mutated retained settings")
	}
}

func TestPersistenceFailureDoesNotInitialize(t *testing.T) {
	store := &memoryStore{fail: true}
	s, _ := New(store, &vpn.Simulator{})
	if _, err := s.Configure(context.Background(), testSettings(), 0); err == nil {
		t.Fatal("ignored persistence failure")
	}
	if s.Status(context.Background()).Initialized {
		t.Fatal("committed failed write")
	}
}

func TestConcurrentReadersAndMutations(t *testing.T) {
	s, _ := New(&memoryStore{}, &vpn.Simulator{})
	ctx := context.Background()
	s.Configure(ctx, testSettings(), 0)
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				status := s.Status(ctx)
				s.Configure(ctx, status.Node.Settings, status.Node.Revision)
			}
		}()
	}
	wg.Wait()
}

type brokenEngine struct {
	vpn.Simulator
	fail bool
}

func (b *brokenEngine) Down(ctx context.Context, n *config.Node) error {
	if b.fail {
		return errors.New("teardown failed")
	}
	return b.Simulator.Down(ctx, n)
}
func TestFailedCleanupRemainsVisible(t *testing.T) {
	engine := &brokenEngine{fail: true}
	s, _ := New(&memoryStore{}, engine)
	settings := testSettings()
	peer, _ := config.New(settings)
	settings.Peers = []config.Peer{{Name: "peer", PublicKey: peer.Public().PublicKey, AllowedIPs: []string{"10.77.0.2/32"}}}
	if _, err := s.Configure(context.Background(), settings, 0); err != nil {
		t.Fatal(err)
	}
	s.Connect(context.Background())
	if err := s.Disconnect(context.Background()); err == nil {
		t.Fatal("lost cleanup failure")
	}
	if !s.Status(context.Background()).Tunnel.Up {
		t.Fatal("reported down despite cleanup failure")
	}
}
