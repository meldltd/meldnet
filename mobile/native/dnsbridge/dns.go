// Package dnsbridge intercepts mobile VPN DNS before WireGuard encryption.
// It uses WireGuard's gVisor netstack for TCP/UDP; no packet or crypto protocol
// is implemented here. Private misses never fall through to public resolvers.
package dnsbridge

import (
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/miekg/dns"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

const Address = "198.18.0.53"

type Policy struct {
	Primary       string   `json:"primary"`
	Pool          string   `json:"pool"`
	Upstreams     []string `json:"upstreams"`
	NetworkHandle uint64   `json:"networkHandle"`
	VPNHandle     uint64   `json:"vpnHandle"`
}

func Parse(data string) (Policy, error) {
	var p Policy
	if len(data) > 4096 || json.Unmarshal([]byte(data), &p) != nil {
		return Policy{}, errors.New("invalid DNS policy")
	}
	pool, e := netip.ParsePrefix(p.Pool)
	primary, pe := netip.ParseAddr(p.Primary)
	if e != nil || pe != nil || !pool.Addr().Is4() || !pool.Addr().IsPrivate() || pool != pool.Masked() || pool.Bits() < 16 || pool.Bits() > 28 || !pool.Contains(primary) || p.NetworkHandle == 0 || len(p.Upstreams) == 0 || len(p.Upstreams) > 8 {
		return Policy{}, errors.New("invalid DNS policy")
	}
	for _, s := range p.Upstreams {
		a, e := netip.ParseAddr(s)
		if e != nil || a.IsUnspecified() || a.IsLoopback() || a.IsMulticast() || s == Address || pool.Contains(a) {
			return Policy{}, errors.New("invalid underlay DNS")
		}
	}
	return p, nil
}
func (p Policy) Private(name string) bool {
	name = strings.ToLower(dns.Fqdn(name))
	if name == "meldnet.internal." || strings.HasSuffix(name, ".meldnet.internal.") {
		return true
	}
	// Reserve the covering octet reverse zone. This conservatively keeps every
	// private /16-/28 pool's reverse misses on the authority, including zone SOA.
	pool, e := netip.ParsePrefix(p.Pool)
	if e != nil {
		return true
	}
	b := pool.Addr().As4()
	zone := strings.Join([]string{itoa(b[1]), itoa(b[0]), "in-addr", "arpa", ""}, ".")
	if pool.Bits() >= 24 {
		zone = itoa(b[2]) + "." + zone
	}
	return name == zone || strings.HasSuffix(name, "."+zone)
}
func itoa(b byte) string {
	const digits = "0123456789"
	if b >= 100 {
		return string([]byte{digits[b/100], digits[(b/10)%10], digits[b%10]})
	}
	if b >= 10 {
		return string([]byte{digits[b/10], digits[b%10]})
	}
	return string(digits[b])
}

type Control func(uint64) func(string, string, syscall.RawConn) error
type Exchange func(*dns.Msg, string, bool, uint64) (*dns.Msg, error)
type Handler struct {
	Policy   func() Policy
	Exchange Exchange
	slots    chan struct{}
}

func NewHandler(policy func() Policy, exchange Exchange) *Handler {
	return &Handler{policy, exchange, make(chan struct{}, 32)}
}
func NetworkExchange(control Control) Exchange {
	return func(q *dns.Msg, server string, private bool, network uint64) (*dns.Msg, error) {
		dialer := &net.Dialer{Timeout: 3 * time.Second}
		dialer.Control = control(network)
		c := &dns.Client{Net: "udp", Timeout: 3 * time.Second, Dialer: dialer}
		reply, _, e := c.Exchange(q, net.JoinHostPort(server, "53"))
		if e == nil && reply.Truncated {
			c.Net = "tcp"
			reply, _, e = c.Exchange(q, net.JoinHostPort(server, "53"))
		}
		return reply, e
	}
}
func (h *Handler) ServeDNS(w dns.ResponseWriter, q *dns.Msg) {
	fail := func(code int) { r := new(dns.Msg); r.SetRcode(q, code); _ = w.WriteMsg(r) }
	if len(q.Question) != 1 || q.Opcode != dns.OpcodeQuery {
		fail(dns.RcodeRefused)
		return
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		fail(dns.RcodeServerFailure)
		return
	}
	p := h.Policy()
	if p.Primary == "" || len(p.Upstreams) == 0 || p.NetworkHandle == 0 {
		fail(dns.RcodeServerFailure)
		return
	}
	private := p.Private(q.Question[0].Name)
	servers := p.Upstreams
	network := p.NetworkHandle
	if private {
		if p.VPNHandle == 0 {
			fail(dns.RcodeServerFailure)
			return
		}
		servers = []string{p.Primary}
		network = p.VPNHandle
	}
	for _, s := range servers {
		r, e := h.Exchange(q, s, private, network)
		if e == nil && r != nil {
			_ = w.WriteMsg(r)
			return
		}
	}
	fail(dns.RcodeServerFailure)
}

type Device struct {
	tun.Device
	stack    tun.Device
	udp, tcp *dns.Server
	writeMu  sync.Mutex
	once     sync.Once
}

func Wrap(base tun.Device, policy func() Policy, control Control) (*Device, error) {
	return wrapHandler(base, NewHandler(policy, NetworkExchange(control)))
}
func wrapHandler(base tun.Device, handler dns.Handler) (*Device, error) {
	st, n, e := netstack.CreateNetTUN([]netip.Addr{netip.MustParseAddr(Address)}, nil, 1280)
	if e != nil {
		return nil, e
	}
	udp, e := n.ListenUDPAddrPort(netip.MustParseAddrPort(Address + ":53"))
	if e != nil {
		st.Close()
		return nil, e
	}
	tcp, e := n.ListenTCPAddrPort(netip.MustParseAddrPort(Address + ":53"))
	if e != nil {
		udp.Close()
		st.Close()
		return nil, e
	}
	d := &Device{Device: base, stack: st}
	d.udp = &dns.Server{PacketConn: udp, Handler: handler, ReadTimeout: 4 * time.Second, WriteTimeout: 4 * time.Second, UDPSize: 4096}
	d.tcp = &dns.Server{Listener: tcp, Handler: handler, ReadTimeout: 4 * time.Second, WriteTimeout: 4 * time.Second, MaxTCPQueries: 16}
	go d.udp.ActivateAndServe()
	go d.tcp.ActivateAndServe()
	go func() {
		// Native Linux TUNs may reserve a virtio header before the packet.
		// Match WireGuard's headroom even when the Android TUN omits it.
		const headroom = 16
		buffers := [][]byte{make([]byte, 65535+headroom)}
		sizes := make([]int, 1)
		for {
			n, e := st.Read(buffers, sizes, headroom)
			if e != nil {
				return
			}
			if n > 0 {
				if _, e = d.Write([][]byte{buffers[0][:headroom+sizes[0]]}, headroom); e != nil {
					return
				}
			}
		}
	}()
	return d, nil
}
func (d *Device) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	for {
		n, e := d.Device.Read(bufs, sizes, offset)
		if e != nil {
			return n, e
		}
		kept := 0
		for i := 0; i < n; i++ {
			p := bufs[i][offset : offset+sizes[i]]
			if len(p) >= 20 && p[0]>>4 == 4 && p[16] == 198 && p[17] == 18 && p[18] == 0 && p[19] == 53 {
				if _, e = d.stack.Write([][]byte{p}, 0); e != nil {
					return 0, e
				}
				continue
			}
			if kept != i {
				copy(bufs[kept][offset:], p)
				sizes[kept] = sizes[i]
			}
			kept++
		}
		if kept > 0 {
			return kept, nil
		}
	}
}
func (d *Device) Write(bufs [][]byte, offset int) (int, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	return d.Device.Write(bufs, offset)
}
func (d *Device) Close() error {
	var e error
	d.once.Do(func() {
		// Close the underlying descriptor first to stop backend reads. Shutting the
		// DNS sockets and stack unblocks outstanding accept/read goroutines.
		e = d.Device.Close()
		_ = d.udp.Shutdown()
		_ = d.tcp.Shutdown()
		_ = d.stack.Close()
	})
	return e
}
