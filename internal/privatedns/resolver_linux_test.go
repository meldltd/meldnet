package privatedns

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/godbus/dbus/v5"
)

// This test uses a disposable private D-Bus daemon, never the host system bus.
// scripts/test-dns-linux.sh supplies the address in a network-isolated container.
type fakeResolved struct {
	mu           sync.Mutex
	index        int32
	servers      []linkDNS
	domains      []linkDomain
	defaultRoute bool
	reverted     bool
	dnssec, dot  string
}

func (f *fakeResolved) SetLinkDNS(i int32, s []linkDNS) *dbus.Error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.index = i
	f.servers = s
	return nil
}
func (f *fakeResolved) SetLinkDomains(i int32, d []linkDomain) *dbus.Error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.domains = d
	return nil
}
func (f *fakeResolved) SetLinkDefaultRoute(i int32, b bool) *dbus.Error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.defaultRoute = b
	return nil
}
func (f *fakeResolved) SetLinkDNSSEC(i int32, s string) *dbus.Error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dnssec = s
	return nil
}
func (f *fakeResolved) SetLinkDNSOverTLS(i int32, s string) *dbus.Error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dot = s
	return nil
}
func (f *fakeResolved) RevertLink(i int32) *dbus.Error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reverted = true
	return nil
}
func TestResolvedDBusLifecycle(t *testing.T) {
	if os.Getenv("MELDNET_TEST_DBUS") != "1" {
		t.Skip("requires isolated test D-Bus")
	}
	address := os.Getenv("DBUS_SYSTEM_BUS_ADDRESS")
	if address != "unix:path=/tmp/meldnet-test-bus" {
		t.Fatal("refusing to use a non-test D-Bus address")
	}
	bus, e := dbus.ConnectSystemBus()
	if e != nil {
		t.Fatal(e)
	}
	defer bus.Close()
	f := &fakeResolved{}
	if e = bus.Export(f, resolvePath, resolveBus+".Manager"); e != nil {
		t.Fatal(e)
	}
	reply, e := bus.RequestName(resolveBus, dbus.NameFlagDoNotQueue)
	if e != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatal("cannot own fake resolved name")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "resolv.conf")
	if e = os.WriteFile(path, []byte("nameserver 127.0.0.53\n"), 0644); e != nil {
		t.Fatal(e)
	}
	files, _ := newFileChange(dir)
	r := &linuxResolver{files: files, path: path}
	if e = r.Apply(context.Background(), "lo", "10.77.0.1", testAuthority(t)); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	if f.index == 0 || len(f.servers) != 1 || !net.IP(f.servers[0].Address).Equal(net.ParseIP("10.77.0.1")) || len(f.domains) != 2 || f.domains[0].Domain != Domain || !f.domains[0].RoutingOnly || f.defaultRoute || f.dnssec != "no" || f.dot != "no" {
		t.Error("incorrect resolved D-Bus settings")
	}
	f.mu.Unlock()
	if r.Mode() != "systemd-resolved split DNS" || r.stub != nil || files.state != nil {
		t.Fatal("resolved mode modified fallback resolver")
	}
	if e = r.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.reverted {
		t.Fatal("resolved link not reverted")
	}
}
