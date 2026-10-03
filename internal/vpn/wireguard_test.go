package vpn

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"meldnet/internal/config"
)

type fakeNetwork struct {
	routes                          map[string]routeSpec
	failAdd, failDelete, failConfig bool
	up                              bool
}

func (f *fakeNetwork) RouteTo(context.Context, netip.Addr) (routeSpec, error) {
	return routeSpec{Prefix: "192.0.2.0/24", Interface: "eth0", Index: 1, Source: "192.0.2.10"}, nil
}
func (f *fakeNetwork) Configure(context.Context, string, []string) (int, error) {
	if f.failConfig {
		return 0, errors.New("address failed")
	}
	f.up = true
	return 10, nil
}
func (f *fakeNetwork) AddRoute(_ context.Context, r routeSpec) error {
	if f.failAdd {
		return errors.New("route failed")
	}
	if _, ok := f.routes[r.Prefix]; ok {
		return errors.New("route exists")
	}
	f.routes[r.Prefix] = r
	return nil
}
func (f *fakeNetwork) DeleteRoute(_ context.Context, r routeSpec) error {
	if f.failDelete {
		return errors.New("delete failed")
	}
	if f.routes[r.Prefix] == r {
		delete(f.routes, r.Prefix)
	}
	return nil
}
func (f *fakeNetwork) IsUp(string) (bool, error) { return f.up, nil }

type fakeDevice struct {
	net             *fakeNetwork
	done            chan struct{}
	ipc, stats      string
	failSet, failUp bool
	closed          int
}

func (f *fakeDevice) IpcSet(s string) error {
	if f.failSet {
		return errors.New("SECRET")
	}
	f.ipc = s
	return nil
}
func (f *fakeDevice) IpcGetOperation(w io.Writer) error {
	_, err := io.WriteString(w, f.stats)
	return err
}
func (f *fakeDevice) Up() error {
	if f.failUp {
		return errors.New("SECRET")
	}
	return nil
}
func (f *fakeDevice) Wait() chan struct{} { return f.done }
func (f *fakeDevice) Close() {
	f.closed++
	if f.closed == 1 {
		close(f.done)
	}
	f.net.up = false
	for k, r := range f.net.routes {
		if r.Interface == "testtun" {
			delete(f.net.routes, k)
		}
	}
}
func testNode(t *testing.T) *config.Node {
	t.Helper()
	n, err := config.New(config.Settings{Name: "node", Addresses: []string{"10.77.0.1/24", "fd77::1/64"}, ListenPort: 51820})
	if err != nil {
		t.Fatal(err)
	}
	peer, _ := config.New(n.Settings)
	n.Settings.Peers = []config.Peer{{Name: "peer", PublicKey: peer.Public().PublicKey, Endpoint: "192.0.2.20:51820", AllowedIPs: []string{"10.77.0.2/32", "fd77::2/128"}, Keepalive: 25}}
	return n
}
func fixture(t *testing.T) (*WireGuard, *fakeNetwork, *fakeDevice) {
	t.Helper()
	network := &fakeNetwork{routes: map[string]routeSpec{}}
	d := &fakeDevice{net: network, done: make(chan struct{})}
	w, err := newWireGuard(t.TempDir(), network, func() (wireDevice, string, error) { return d, "testtun", nil })
	if err != nil {
		t.Fatal(err)
	}
	return w, network, d
}
func TestEmbeddedLifecycleAndPrivateStatus(t *testing.T) {
	w, network, d := fixture(t)
	n := testNode(t)
	ctx := context.Background()
	raw, _ := base64.StdEncoding.DecodeString(n.Settings.Peers[0].PublicKey)
	d.stats = "private_key=SECRET\npublic_key=" + hex.EncodeToString(raw) + "\npreshared_key=SECRET\nlast_handshake_time_sec=1720000000\nrx_bytes=100\ntx_bytes=200\n"
	if err := w.Up(ctx, n); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.ipc, "endpoint=192.0.2.20:51820") || strings.Contains(d.ipc, "[Interface]") {
		t.Fatal("incorrect in-process configuration")
	}
	if len(network.routes) != 2 {
		t.Fatal("did not install both address-family routes")
	}
	status, err := w.Status(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Up || status.Peers[0].Received != 100 || status.Peers[0].Sent != 200 || status.Peers[0].LastHandshake == nil {
		t.Fatalf("bad public status: %+v", status)
	}
	data, _ := json.Marshal(status)
	if strings.Contains(string(data), "SECRET") || strings.Contains(string(data), n.PrivateKey) {
		t.Fatal("secret leaked into public status")
	}
	if err := w.Up(ctx, n); err != nil {
		t.Fatal("idempotent up", err)
	}
	if err := w.Down(ctx, n); err != nil {
		t.Fatal(err)
	}
	if d.closed != 1 || len(network.routes) != 0 {
		t.Fatal("device/routes not cleaned")
	}
	if err := w.Down(ctx, n); err != nil {
		t.Fatal("idempotent down", err)
	}
}
func TestFullTunnelEndpointPinAndCrashCleanup(t *testing.T) {
	w, network, d := fixture(t)
	n := testNode(t)
	n.Settings.Peers[0].AllowedIPs = []string{"0.0.0.0/0", "::/0"}
	if err := w.Up(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if len(network.routes) != 5 {
		t.Fatalf("want 4 split defaults + pinned endpoint, got %d", len(network.routes))
	}
	data, err := os.ReadFile(w.journal)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), n.PrivateKey) {
		t.Fatal("secret in route journal")
	}
	d.Close() // Model OS removal of TUN and its routes when the process dies.
	if len(network.routes) != 1 {
		t.Fatal("expected physical endpoint route to survive crash")
	}
	recovered, err := newWireGuard(filepath.Dir(w.journal), network, w.create)
	if err != nil {
		t.Fatal(err)
	}
	if len(network.routes) != 0 {
		t.Fatal("restart did not recover endpoint routes")
	}
	s, err := recovered.Status(context.Background(), n)
	if err != nil || s.Up {
		t.Fatal("restart must remain disconnected", err)
	}
	if _, err := os.Stat(w.journal); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("journal not removed")
	}
}
func TestFailureRollbackAndRetry(t *testing.T) {
	for _, step := range []string{"config", "set", "route", "up"} {
		t.Run(step, func(t *testing.T) {
			w, network, d := fixture(t)
			n := testNode(t)
			network.failConfig = step == "config"
			network.failAdd = step == "route"
			d.failSet = step == "set"
			d.failUp = step == "up"
			if err := w.Up(context.Background(), n); err == nil || strings.Contains(err.Error(), "SECRET") {
				t.Fatal("failed operation or diagnostic handling", err)
			}
			if w.device != nil || d.closed != 1 || len(network.routes) != 0 {
				t.Fatal("partial setup leaked a device or route")
			}
		})
	}
	w, network, _ := fixture(t)
	n := testNode(t)
	n.Settings.Peers[0].AllowedIPs = []string{"0.0.0.0/0"}
	if err := w.Up(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	network.failDelete = true
	if err := w.Down(context.Background(), n); err == nil {
		t.Fatal("lost cleanup failure")
	}
	if _, err := os.Stat(w.journal); err != nil {
		t.Fatal("lost recovery journal")
	}
	if _, err := w.Status(context.Background(), n); err == nil {
		t.Fatal("failed cleanup not visible")
	}
	network.failDelete = false
	if err := w.Down(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if len(network.routes) != 0 {
		t.Fatal("retry left routes")
	}
}
func TestRoutePreflightBeforeDeviceCreation(t *testing.T) {
	for _, kind := range []string{"dns", "endpoint-loop", "full-inbound", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			w, _, _ := fixture(t)
			n := testNode(t)
			ctx := context.Background()
			w.create = func() (wireDevice, string, error) {
				t.Fatal("created device before rejecting invalid routing")
				return nil, "", nil
			}
			switch kind {
			case "dns":
				n.Settings.Peers[0].Endpoint = "bad.example:1234"
				w.resolve = func(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("DNS failed") }
			case "endpoint-loop":
				n.Settings.Peers[0].AllowedIPs = []string{"192.0.2.20/32"}
			case "full-inbound":
				n.Settings.Peers[0].Endpoint = ""
				n.Settings.Peers[0].AllowedIPs = []string{"0.0.0.0/0"}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err := w.Up(ctx, n); err == nil {
				t.Fatal("accepted invalid setup")
			}
		})
	}
}
func TestStatusParserChunkBoundaries(t *testing.T) {
	n := testNode(t)
	raw, _ := base64.StdEncoding.DecodeString(n.Settings.Peers[0].PublicKey)
	w := newStatusWriter(n)
	data := "private_key=SECRET\npublic_key=" + hex.EncodeToString(raw) + "\nrx_bytes=42\npreshared_key=SECRET\ntx_bytes=7\n"
	for _, b := range []byte(data) {
		if _, err := w.Write([]byte{b}); err != nil {
			t.Fatal(err)
		}
	}
	if w.err != nil || w.peers[n.Settings.Peers[0].PublicKey].Received != 42 {
		t.Fatal("chunked status parse failed")
	}
	if strings.Contains(string(w.line[:cap(w.line)]), "SECRET") {
		t.Fatal("secret left in parser buffer")
	}
}
