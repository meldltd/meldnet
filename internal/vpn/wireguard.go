package vpn

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
	"meldnet/internal/config"
	"meldnet/internal/securefs"
)

type wireDevice interface {
	IpcSet(string) error
	IpcGetOperation(io.Writer) error
	Up() error
	Close()
	Wait() chan struct{}
}
type deviceFactory func() (wireDevice, string, error)

type WireGuard struct {
	mu      sync.Mutex
	network network
	create  deviceFactory
	resolve func(context.Context, string) ([]netip.Addr, error)
	device  wireDevice
	name    string
	running bool
	journal string
	pending []routeSpec
	lock    *os.File
	relay   *config.Node
}

// NewWireGuard is the single-tunnel compatibility constructor.
func NewWireGuard(dir string) (*WireGuard, error) {
	owner, err := NewOwner()
	if err != nil {
		return nil, err
	}
	w, err := owner.NewWireGuard(dir, "default")
	if err != nil {
		owner.Close()
		return nil, err
	}
	w.lock = owner.lock
	return w, nil
}

// Owner holds the one process-wide networking lock while allowing independent
// devices/journals inside that process. The daemon closes devices before Owner.
type Owner struct{ lock *os.File }

func NewOwner() (*Owner, error) {
	path, err := networkLockPath()
	if err != nil {
		return nil, err
	}
	lock, err := securefs.Lock(path)
	if err != nil {
		return nil, err
	}
	return &Owner{lock: lock}, nil
}
func (o *Owner) Close() error { return o.lock.Close() }
func (o *Owner) NewWireGuard(dir, id string) (*WireGuard, error) {
	if err := securefs.Directory(dir); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(filepath.Join(dir, "meldnet.conf")); err == nil {
		return nil, errors.New("legacy wg-quick runtime state found: disconnect using the previous Meldnet version before upgrading; do not discard a live tunnel's state")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var w *WireGuard
	var err error
	w, err = newWireGuard(dir, newNetwork(), func() (wireDevice, string, error) {
		name := tunnelName()
		if name != "utun" && id != "default" {
			h := sha256.Sum256([]byte(id))
			name = fmt.Sprintf("meldnet-%x", h[:3])
		}
		if name != "utun" {
			interfaces, err := net.Interfaces()
			if err != nil {
				return nil, "", fmt.Errorf("inspect existing interfaces: %w", err)
			}
			for _, iface := range interfaces {
				if iface.Name == name {
					return nil, "", fmt.Errorf("interface %s already exists; refusing to attach to an unmanaged device", name)
				}
			}
		}
		t, err := tun.CreateTUN(name, tunnelMTU)
		if err != nil {
			return nil, "", fmt.Errorf("create OS TUN device (requires network privileges and TUN support): %w", err)
		}
		name, err = t.Name()
		if err != nil {
			t.Close()
			return nil, "", errors.New("cannot read TUN interface name")
		}
		if w.relay != nil {
			t = newRelayTUN(t, w.relay)
		}
		// No external UAPI socket or library log sink is exposed.
		d := device.NewDevice(t, conn.NewDefaultBind(), device.NewLogger(device.LogLevelSilent, ""))
		return d, name, nil
	})
	if err != nil {
		return nil, err
	}
	return w, nil
}

func newWireGuard(dir string, n network, create deviceFactory) (*WireGuard, error) {
	w := &WireGuard{network: n, create: create, journal: filepath.Join(dir, "routes.json")}
	resolver := &net.Resolver{PreferGo: true}
	w.resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
		return resolver.LookupNetIP(ctx, "ip", host)
	}
	data, err := securefs.Read(w.journal)
	if err == nil {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&w.pending); err != nil {
			return nil, errors.New("invalid endpoint route journal")
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			return nil, errors.New("trailing endpoint route journal data")
		}
		if len(w.pending) > 128 {
			return nil, errors.New("too many journaled endpoint routes")
		}
		for _, r := range w.pending {
			p, err := netip.ParsePrefix(r.Prefix)
			if err != nil || p.Bits() != p.Addr().BitLen() || r.Index <= 0 || r.Interface == "" {
				return nil, errors.New("invalid journaled endpoint route")
			}
			for _, ip := range []string{r.Gateway, r.Source} {
				if ip != "" {
					if _, err := netip.ParseAddr(ip); err != nil {
						return nil, errors.New("invalid journaled route address")
					}
				}
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Failed recovery remains visible through Status and retryable via Down.
		_ = w.cleanup(ctx)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return w, nil
}

func (*WireGuard) Kind() string { return "wireguard" }

func (w *WireGuard) Status(ctx context.Context, n *config.Node) (State, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := State{Peers: []PeerStatus{}}
	if err := ctx.Err(); err != nil {
		return s, err
	}
	if w.device == nil {
		if len(w.pending) > 0 {
			return s, errors.New("endpoint route cleanup pending; disconnect to retry")
		}
		return s, nil
	}
	s.Interface = w.name
	select {
	case <-w.device.Wait():
		return s, errors.New("embedded WireGuard device closed unexpectedly; disconnect and reconnect")
	default:
	}
	up, err := w.network.IsUp(w.name)
	if err != nil || !up || !w.running {
		return s, errors.New("TUN interface is unavailable or down; disconnect and reconnect")
	}
	s.Up = true
	writer := newStatusWriter(n)
	if err := w.device.IpcGetOperation(writer); err != nil {
		return s, errors.New("could not read embedded WireGuard status")
	}
	if writer.err != nil || len(writer.line) > 0 {
		return s, errors.New("invalid embedded WireGuard status")
	}
	for _, p := range n.Settings.Peers {
		s.Peers = append(s.Peers, *writer.peers[p.PublicKey])
	}
	return s, nil
}

func (w *WireGuard) Up(ctx context.Context, n *config.Node) (err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err = n.Validate(); err != nil {
		return err
	}
	if w.device != nil {
		select {
		case <-w.device.Wait():
			return errors.New("embedded device closed; disconnect before reconnecting")
		default:
		}
		up, e := w.network.IsUp(w.name)
		if e != nil || !up || !w.running {
			return errors.New("interface is not healthy; disconnect before reconnecting")
		}
		return nil
	}
	if err = w.cleanup(ctx); err != nil {
		return err
	}
	var nets []string
	full := false
	for _, p := range n.Settings.Peers {
		for _, raw := range p.AllowedIPs {
			nets = append(nets, raw)
			if netip.MustParsePrefix(raw).Bits() == 0 {
				full = true
			}
		}
	}
	prefixes := tunnelPrefixes(nets)
	endpoints := map[string]string{}
	var bypass []routeSpec
	seen := map[string]bool{}
	for _, p := range n.Settings.Peers {
		if p.Endpoint == "" {
			if full {
				return errors.New("default-route tunnels require explicit, stable endpoints for every peer")
			}
			continue
		}
		host, port, _ := net.SplitHostPort(p.Endpoint)
		var ips []netip.Addr
		if ip, e := netip.ParseAddr(host); e == nil {
			ips = []netip.Addr{ip}
		} else {
			var e error
			ips, e = w.resolve(ctx, host)
			if e != nil {
				return errors.New("could not resolve peer endpoint")
			}
		}
		var chosen netip.Addr
		var path routeSpec
		for _, ip := range ips {
			ip = ip.Unmap()
			if !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() {
				continue
			}
			candidate, e := w.network.RouteTo(ctx, ip)
			if e == nil {
				chosen = ip
				path = candidate
				break
			}
		}
		if !chosen.IsValid() {
			return errors.New("no usable existing route to peer endpoint")
		}
		endpoints[p.PublicKey] = net.JoinHostPort(chosen.String(), port)
		covered := false
		for _, prefix := range prefixes {
			if prefix.Contains(chosen) {
				if prefix.Bits() == chosen.BitLen() {
					return errors.New("a peer endpoint cannot also be a tunneled host route")
				}
				covered = true
			}
		}
		if covered && !seen[chosen.String()] {
			seen[chosen.String()] = true
			// An existing host route already outranks every tunnel prefix.
			existing, e := netip.ParsePrefix(path.Prefix)
			if e == nil && existing.Bits() == chosen.BitLen() {
				continue
			}
			path.Prefix = netip.PrefixFrom(chosen, chosen.BitLen()).String()
			bypass = append(bypass, path)
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	w.device, w.name, err = w.create()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			rollback, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err = errors.Join(err, w.cleanup(rollback))
		}
	}()
	if err = w.device.IpcSet(renderIPC(n, endpoints)); err != nil {
		return errors.New("embedded WireGuard rejected configuration")
	}
	index, err := w.network.Configure(ctx, w.name, n.Settings.Addresses)
	if err != nil {
		return fmt.Errorf("configure TUN addresses: %w", err)
	}
	for _, r := range bypass {
		// Persist intent before modifying a physical interface's routing state.
		w.pending = append(w.pending, r)
		if err = w.saveJournal(); err != nil {
			return err
		}
		if err = w.network.AddRoute(ctx, r); err != nil {
			return fmt.Errorf("pin peer endpoint route: %w", err)
		}
	}
	for _, prefix := range prefixes {
		source := ""
		for _, raw := range n.Settings.Addresses {
			a := netip.MustParsePrefix(raw).Addr()
			if a.Is4() == prefix.Addr().Is4() {
				source = a.String()
				break
			}
		}
		if source == "" {
			return fmt.Errorf("add an interface address for the IP family of %s", prefix)
		}
		r := routeSpec{Prefix: prefix.String(), Interface: w.name, Index: index, Source: source}
		if err = w.network.AddRoute(ctx, r); err != nil {
			return fmt.Errorf("install tunnel route: %w", err)
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = w.device.Up(); err != nil {
		return errors.New("could not start embedded WireGuard UDP transport; check listen-port conflicts")
	}
	w.running = true
	return nil
}

func (w *WireGuard) saveJournal() error {
	data, err := json.Marshal(w.pending)
	if err != nil {
		return err
	}
	if err := securefs.Write(w.journal, data); err != nil {
		return errors.New("could not persist endpoint route recovery journal")
	}
	return nil
}

func (w *WireGuard) cleanup(ctx context.Context) error {
	// Non-persistent TUN closure removes its addresses and attached routes.
	if w.device != nil {
		w.device.Close()
		w.device = nil
		w.name = ""
		w.running = false
	}
	var pending []routeSpec
	var failures []error
	for i := len(w.pending) - 1; i >= 0; i-- {
		r := w.pending[i]
		if err := w.network.DeleteRoute(ctx, r); err != nil {
			pending = append(pending, r)
			failures = append(failures, fmt.Errorf("remove endpoint route: %w", err))
		}
	}
	w.pending = pending
	if len(pending) > 0 {
		failures = append(failures, w.saveJournal())
	} else if err := os.Remove(w.journal); err != nil && !errors.Is(err, os.ErrNotExist) {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}
func (w *WireGuard) Down(ctx context.Context, _ *config.Node) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cleanup(ctx)
}
func (w *WireGuard) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := w.Down(ctx, nil)
	if w.lock != nil {
		err = errors.Join(err, w.lock.Close())
		w.lock = nil
	}
	return err
}

func renderIPC(n *config.Node, endpoints map[string]string) string {
	key, _ := base64.StdEncoding.DecodeString(n.PrivateKey)
	var b strings.Builder
	fmt.Fprintf(&b, "private_key=%s\nlisten_port=%d\nreplace_peers=true\n", hex.EncodeToString(key), n.Settings.ListenPort)
	for _, p := range n.Settings.Peers {
		key, _ := base64.StdEncoding.DecodeString(p.PublicKey)
		fmt.Fprintf(&b, "public_key=%s\nreplace_allowed_ips=true\npersistent_keepalive_interval=%d\n", hex.EncodeToString(key), p.Keepalive)
		if endpoint := endpoints[p.PublicKey]; endpoint != "" {
			fmt.Fprintf(&b, "endpoint=%s\n", endpoint)
		}
		for _, prefix := range p.AllowedIPs {
			fmt.Fprintf(&b, "allowed_ip=%s\n", prefix)
		}
	}
	return b.String()
}

func (w *WireGuard) RecoveryPending() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.pending) > 0
}
