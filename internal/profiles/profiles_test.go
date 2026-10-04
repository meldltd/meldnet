package profiles

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"meldnet/internal/config"
	"meldnet/internal/control"
	"meldnet/internal/service"
	"meldnet/internal/vpn"
)

func factory(t *testing.T) Factory {
	t.Helper()
	gate := vpn.NewCoordinator()
	return func(id, dir string, o *control.Options) (*Runtime, error) {
		store, err := config.Open(dir)
		if err != nil {
			return nil, err
		}
		e := gate.Wrap(id, &vpn.Simulator{})
		s, err := service.New(store, e)
		if err != nil {
			return nil, err
		}
		m, err := control.New(dir, s, o)
		if err != nil {
			return nil, err
		}
		return &Runtime{Service: s, Manager: m, Engine: e, Close: func() error { return nil }}, nil
	}
}
func primary(t *testing.T, pool string) (*control.Manager, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	dir := t.TempDir()
	f := factory(t)
	r, err := f("default", dir, &control.Options{Listen: addr, URL: "https://" + addr, Endpoint: "127.0.0.1:51820", Pool: pool, Name: "primary"})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Manager.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Manager.Serve(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	key, err := r.Manager.Invite()
	if err != nil {
		t.Fatal(err)
	}
	// Wait only for the test listener, with a bounded timeout.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, e := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if e == nil {
			conn.Close()
			return r.Manager, key
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("primary listener not ready")
	return nil, ""
}
func get(t *testing.T, m *Manager, id string) Profile {
	t.Helper()
	p, err := m.Profile(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestIndependentNetworksAndRestartPolicy(t *testing.T) {
	ctx := context.Background()
	_, keyA := primary(t, "10.77.0.0/24")
	_, keyB := primary(t, "10.88.0.0/24")
	_, keyC := primary(t, "10.77.0.0/25")
	dir := t.TempDir()
	m, err := New(dir, factory(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []JoinRequest{{ID: "work", Name: "laptop", Key: keyA, AutoConnect: true, Connect: true}, {ID: "home", Name: "laptop", Key: keyB, Connect: true}} {
		if err = m.Join(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	a, b := get(t, m, "work"), get(t, m, "home")
	if !a.Status.Tunnel.Up || !b.Status.Tunnel.Up {
		t.Fatal("non-overlapping networks not both up")
	}
	if a.Status.Node.PublicKey == b.Status.Node.PublicKey {
		t.Fatal("profiles share identity")
	}
	if err = m.Join(ctx, JoinRequest{ID: "overlap", Name: "laptop", Key: keyC, Connect: true}); !errors.Is(err, vpn.ErrOverlap) {
		t.Fatalf("expected overlap: %v", err)
	}
	conflict := get(t, m, "overlap")
	if conflict.Status.Tunnel.Up || conflict.Warning == "" {
		t.Fatal("overlap warning/up status incorrect")
	}
	if len(conflict.Network.Members) == 0 {
		t.Fatal("enrollment not saved on conflict")
	}
	if err = m.Disconnect(ctx, "work"); err != nil {
		t.Fatal(err)
	}
	if !get(t, m, "home").Status.Tunnel.Up {
		t.Fatal("home stopped when work disconnected")
	}
	if err = m.Connect(ctx, "overlap"); err != nil {
		t.Fatal(err)
	}
	if err = m.SetAuto("home", false); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(m.List(ctx))
	for _, key := range []string{keyA, keyB, keyC, "PrivateKey", "Credential", "Enrollment"} {
		if strings.Contains(string(raw), key) {
			t.Fatal("private data in API")
		}
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	restart, err := New(dir, factory(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	run, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { restart.Run(run); close(done) }()
	defer func() { cancel(); <-done; restart.Close() }()
	deadline := time.Now().Add(4 * time.Second)
	for !get(t, restart, "work").Status.Tunnel.Up && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !get(t, restart, "work").Status.Tunnel.Up || get(t, restart, "home").Status.Tunnel.Up || get(t, restart, "overlap").Status.Tunnel.Up {
		t.Fatal("startup flags not respected")
	}
	if get(t, restart, "work").Status.Node.PublicKey != a.Status.Node.PublicKey {
		t.Fatal("identity changed after restart")
	}
	info, err := os.Stat(filepath.Join(dir, "profiles.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unsafe profile metadata permissions")
	}
}
func TestLegacyEnrollmentMigration(t *testing.T) {
	_, key := primary(t, "10.90.0.0/24")
	dir := t.TempDir()
	f := factory(t)
	r, err := f("default", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Manager.Join(context.Background(), control.JoinRequest{Name: "old-device", Key: key}); err != nil {
		t.Fatal(err)
	}
	before := r.Service.Status(context.Background()).Node.PublicKey
	r.Service.Disconnect(context.Background())
	m, err := New(dir, factory(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	p := get(t, m, "default")
	if !p.AutoConnect || p.Status.Node.PublicKey != before || p.Network.Role != "client" {
		t.Fatal("legacy identity/policy lost")
	}
	if err = m.Connect(context.Background(), "default"); err != nil {
		t.Fatal(err)
	}
	if err = m.SetAuto("default", false); err != nil {
		t.Fatal(err)
	}
	m.Close()
	next, err := New(dir, factory(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if get(t, next, "default").AutoConnect {
		t.Fatal("explicit opt-out lost during migration")
	}
}
func TestInvalidProfileNamesDoNotCreateState(t *testing.T) {
	m, err := New(t.TempDir(), factory(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	for _, id := range []string{"../escape", "Upper", "", "x/y", "bad-"} {
		if m.Join(context.Background(), JoinRequest{ID: id}) == nil {
			t.Fatal("invalid ID allowed")
		}
	}
	if len(m.List(context.Background())) != 1 {
		t.Fatal("invalid join added a profile")
	}
}
