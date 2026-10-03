package service

import (
	"context"
	"errors"
	"testing"

	"meldnet/internal/config"
	"meldnet/internal/privatedns"
	"meldnet/internal/vpn"
)

type testDNS struct {
	engine   *vpn.Simulator
	node     *config.Node
	up, down int
	failure  error
}

func (d *testDNS) Configure(privatedns.Settings) error { return nil }
func (d *testDNS) Up(ctx context.Context, _ string) error {
	state, _ := d.engine.Status(ctx, d.node)
	if !state.Up {
		return errors.New("DNS started before tunnel")
	}
	d.up++
	return d.failure
}
func (d *testDNS) Down(ctx context.Context) error {
	state, _ := d.engine.Status(ctx, d.node)
	if !state.Up {
		return errors.New("DNS stopped after tunnel")
	}
	d.down++
	return d.failure
}
func (d *testDNS) Status() privatedns.Status {
	s := privatedns.Status{Domain: privatedns.Domain}
	if d.failure != nil {
		s.Error = d.failure.Error()
	}
	return s
}
func TestDNSLifecycleAndFailureVisibility(t *testing.T) {
	ctx := context.Background()
	store, e := config.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	engine := &vpn.Simulator{}
	s, e := New(store, engine)
	if e != nil {
		t.Fatal(e)
	}
	s.SetManaged()
	node, e := config.New(config.Settings{Name: "primary", Addresses: []string{"10.77.0.1/32"}})
	if e != nil {
		t.Fatal(e)
	}
	dns := &testDNS{engine: engine, node: node}
	s.SetDNS(dns)
	if _, e = s.Reconcile(ctx, node.Settings, true); e != nil {
		t.Fatal(e)
	}
	if dns.up != 1 {
		t.Fatal("DNS not started")
	}
	if e = s.Disconnect(ctx); e != nil {
		t.Fatal(e)
	}
	if dns.down != 1 {
		t.Fatal("DNS not restored")
	}
	if _, e = s.Reconcile(ctx, node.Settings, true); e != nil {
		t.Fatal(e)
	}
	if dns.up != 1 {
		t.Fatal("paused controller reenabled DNS")
	}
	dns.failure = errors.New("resolver conflict")
	if e = s.Connect(ctx); e != nil {
		t.Fatal(e)
	}
	status := s.Status(ctx)
	if !status.Tunnel.Up || status.DNS == nil || status.DNS.Error != "resolver conflict" {
		t.Fatal("DNS failure should be visible without disabling VPN")
	}
	if e = s.Disconnect(ctx); e == nil {
		t.Fatal("cleanup failure hidden")
	}
	if s.Status(ctx).Tunnel.Up {
		t.Fatal("cleanup error prevented tunnel teardown")
	}
}
