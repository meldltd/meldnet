package privatedns

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const loopbackDNS = "127.77.0.1"
const resolveBus = "org.freedesktop.resolve1"
const resolvePath = dbus.ObjectPath("/org/freedesktop/resolve1")

type linkDNS struct {
	Family  int32
	Address []byte
}
type linkDomain struct {
	Domain      string
	RoutingOnly bool
}
type linuxResolver struct {
	files *fileChange
	path  string
	stub  *server
	proxy *forwarder
	bus   *dbus.Conn
	index int
	mode  string
	grace *time.Timer
}

func newResolver(dir string) (resolver, error) {
	f, e := newFileChange(dir)
	if e != nil {
		return nil, e
	}
	if e = f.restore(); e != nil {
		return nil, e
	}
	return &linuxResolver{files: f, path: "/etc/resolv.conf"}, nil
}
func (r *linuxResolver) Mode() string { return r.mode }
func (r *linuxResolver) Apply(ctx context.Context, iface, primary string, z *authority) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if r.bus != nil {
		return r.configureResolved(ctx, iface, primary, z)
	}
	if r.stub != nil && r.files.state != nil {
		if e := r.stub.health(); e != nil {
			return e
		}
		if e := r.files.check(); e != nil {
			return e
		}
		r.proxy.suspended.Store(false)
		return nil
	}
	if r.stub != nil {
		if r.grace != nil {
			r.grace.Stop()
			r.grace = nil
		}
		_ = r.stub.close()
		r.stub = nil
	}

	path, e := filepath.EvalSymlinks(r.path)
	if e != nil {
		return e
	}
	content, e := readResolver(path)
	if e != nil {
		return e
	}
	up, e := upstreams(content)
	if e != nil {
		return e
	}
	// Only prefer resolved when applications actually use its stub. Systems with
	// no resolved service need no new dependency: the embedded proxy is a fallback.
	for _, ip := range up {
		if ip == "127.0.0.53" {
			bus, err := dbus.ConnectSystemBus()
			if err == nil {
				var owner bool
				err = bus.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.NameHasOwner", 0, resolveBus).Store(&owner)
				if err == nil && owner {
					r.bus = bus
					if err = r.configureResolved(ctx, iface, primary, z); err != nil {
						return err
					}
					r.mode = "systemd-resolved split DNS"
					return nil
				}
				bus.Close()
			}
		}
	}
	addresses := make([]string, 0, len(up))
	for _, ip := range up {
		addresses = append(addresses, net.JoinHostPort(ip, "53"))
	}
	r.proxy = &forwarder{zone: z, privateServer: net.JoinHostPort(primary, "53"), upstreams: addresses}
	stub, e := serve(net.JoinHostPort(loopbackDNS, "53"), r.proxy)
	if e != nil {
		return errors.New("cannot start local private DNS proxy: " + e.Error())
	}
	r.stub = stub
	var lines []string
	lines = append(lines, marker, "nameserver "+loopbackDNS)
	for _, line := range strings.Split(string(content), "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && f[0] == "nameserver" {
			continue
		}
		lines = append(lines, line)
	}
	replacement := []byte(strings.Join(lines, "\n"))
	if e = r.files.install(path, replacement, true); e != nil {
		// Restore first: a failed write may already have pointed DNS at the proxy.
		restore := r.files.restore()
		if restore == nil {
			_ = r.stub.close()
			r.stub = nil
		}
		return errors.Join(e, restore)
	}
	r.mode = "embedded DNS proxy"
	return nil
}
func (r *linuxResolver) configureResolved(ctx context.Context, iface, primary string, z *authority) error {
	i, e := net.InterfaceByName(iface)
	if e != nil {
		return e
	}
	if r.index != 0 && r.index != i.Index {
		return errors.New("DNS interface changed before resolver cleanup")
	}
	r.index = i.Index
	obj := r.bus.Object(resolveBus, resolvePath)
	index := int32(i.Index)
	for _, call := range []struct {
		name string
		args []any
	}{
		{"SetLinkDNS", []any{index, []linkDNS{{Family: 2, Address: net.ParseIP(primary).To4()}}}},
		{"SetLinkDomains", []any{index, []linkDomain{{Domain: Domain, RoutingOnly: true}, {Domain: reverseDomain(z.current.Load().pool), RoutingOnly: true}}}},
		{"SetLinkDefaultRoute", []any{index, false}},
		// The private authoritative zone is deliberately unsigned and transported
		// inside WireGuard; do not apply global DNSSEC/DoT requirements to this link.
		{"SetLinkDNSSEC", []any{index, "no"}},
		{"SetLinkDNSOverTLS", []any{index, "no"}},
	} {
		if e = obj.CallWithContext(ctx, resolveBus+".Manager."+call.name, 0, call.args...).Err; e != nil {
			return errors.New("configure systemd-resolved private DNS: " + e.Error())
		}
	}
	r.mode = "systemd-resolved split DNS"
	return nil
}
func (r *linuxResolver) Restore(ctx context.Context) error {
	if r.proxy != nil {
		r.proxy.suspended.Store(true)
	}
	if r.bus != nil {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if r.index != 0 {
			e := r.bus.Object(resolveBus, resolvePath).CallWithContext(ctx, resolveBus+".Manager.RevertLink", 0, int32(r.index)).Err
			if e != nil {
				if _, exists := net.InterfaceByIndex(r.index); exists == nil {
					return e
				}
			}
		}
		r.bus.Close()
		r.bus = nil
		r.index = 0
	}
	if e := r.files.restore(); e != nil {
		return e
	}
	if r.stub != nil && r.grace == nil {
		// Go's resolver caches resolv.conf for up to five seconds. Keep forwarding
		// through the old loopback socket briefly after restoring the file, so an
		// immediate reconnect can resolve the public endpoint. The timer captures
		// only this server, never a future replacement or mutable resolver state.
		old := r.stub
		r.grace = time.AfterFunc(6*time.Second, func() { _ = old.close() })
	}

	r.mode = ""
	return nil
}
