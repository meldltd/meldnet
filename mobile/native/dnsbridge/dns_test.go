package dnsbridge

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

func policyFixture() Policy {
	return Policy{Primary: "10.77.0.1", Pool: "10.77.0.0/24", Upstreams: []string{"192.168.1.1"}, NetworkHandle: 42, VPNHandle: 43}
}
func TestPolicy(t *testing.T) {
	good := `{"primary":"10.77.0.1","pool":"10.77.0.0/24","upstreams":["192.168.1.1"],"networkHandle":42}`
	if _, e := Parse(good); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{`{}`, `{"primary":"10.77.0.1","pool":"10.77.0.0/24","upstreams":["198.18.0.53"],"networkHandle":42}`, `{"primary":"10.77.0.1","pool":"10.77.0.0/24","upstreams":["10.77.0.2"],"networkHandle":42}`} {
		if _, e := Parse(bad); e == nil {
			t.Fatal("unsafe policy")
		}
	}
	p := policyFixture()
	for _, name := range []string{"unknown.meldnet.internal.", "MELDNET.INTERNAL", "2.0.77.10.in-addr.arpa.", "0.77.10.in-addr.arpa."} {
		if !p.Private(name) {
			t.Fatal(name)
		}
	}
	for _, name := range []string{"example.org.", "meldnet.internal.example.org.", "2.0.78.10.in-addr.arpa."} {
		if p.Private(name) {
			t.Fatal(name)
		}
	}
}

type writer struct{ reply *dns.Msg }

func (w *writer) LocalAddr() net.Addr         { return &net.UDPAddr{} }
func (w *writer) RemoteAddr() net.Addr        { return &net.UDPAddr{} }
func (w *writer) WriteMsg(m *dns.Msg) error   { w.reply = m; return nil }
func (w *writer) Write(b []byte) (int, error) { return len(b), nil }
func (w *writer) Close() error                { return nil }
func (w *writer) TsigStatus() error           { return nil }
func (w *writer) TsigTimersOnly(bool)         {}
func (w *writer) Hijack()                     {}
func TestPrivateMissAndFailureNeverFallThrough(t *testing.T) {
	for _, failure := range []bool{false, true} {
		var servers []string
		h := NewHandler(policyFixture, func(q *dns.Msg, server string, private bool, n uint64) (*dns.Msg, error) {
			servers = append(servers, server)
			if !private || n != 43 {
				t.Error("wrong policy")
			}
			if failure {
				return nil, errors.New("unavailable")
			}
			r := new(dns.Msg)
			r.SetRcode(q, dns.RcodeNameError)
			return r, nil
		})
		q := new(dns.Msg)
		q.SetQuestion("missing.meldnet.internal.", dns.TypeA)
		w := new(writer)
		h.ServeDNS(w, q)
		if len(servers) != 1 || servers[0] != "10.77.0.1" {
			t.Fatal(servers)
		}
		expected := dns.RcodeNameError
		if failure {
			expected = dns.RcodeServerFailure
		}
		if w.reply.Rcode != expected {
			t.Fatal(w.reply)
		}
	}
}
func TestPublicUsesUnderlay(t *testing.T) {
	h := NewHandler(policyFixture, func(q *dns.Msg, s string, p bool, n uint64) (*dns.Msg, error) {
		if p || s != "192.168.1.1" || n != 42 {
			t.Error("public policy")
		}
		r := new(dns.Msg)
		r.SetReply(q)
		return r, nil
	})
	q := new(dns.Msg)
	q.SetQuestion("example.org.", dns.TypeA)
	w := new(writer)
	h.ServeDNS(w, q)
	if w.reply == nil {
		t.Fatal("no reply")
	}
}

type packetTun struct {
	incoming chan []byte
	done     chan struct{}
	events   chan tun.Event
	client   tun.Device
	once     sync.Once
}

func (p *packetTun) File() *os.File           { return nil }
func (p *packetTun) MTU() (int, error)        { return 1280, nil }
func (p *packetTun) Name() (string, error)    { return "dns-test", nil }
func (p *packetTun) Events() <-chan tun.Event { return p.events }
func (p *packetTun) BatchSize() int           { return 1 }
func (p *packetTun) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	select {
	case b := <-p.incoming:
		sizes[0] = copy(bufs[0][offset:], b)
		return 1, nil
	case <-p.done:
		return 0, os.ErrClosed
	}
}
func (p *packetTun) Write(bufs [][]byte, offset int) (int, error) {
	// Model the kernel TUN headroom requirement, which the pure netstack
	// otherwise silently accepts at offset zero.
	if offset < 16 {
		return 0, errors.New("DNS response lacks native TUN headroom")
	}
	return p.client.Write(bufs, offset)
}
func (p *packetTun) Close() error { p.once.Do(func() { close(p.done); close(p.events) }); return nil }
func TestUDPAndTCPThroughInterceptedPackets(t *testing.T) {
	client, network, e := netstack.CreateNetTUN([]netip.Addr{netip.MustParseAddr("10.77.0.2")}, nil, 1280)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	base := &packetTun{incoming: make(chan []byte, 32), done: make(chan struct{}), events: make(chan tun.Event), client: client}
	h := NewHandler(policyFixture, func(q *dns.Msg, _ string, _ bool, _ uint64) (*dns.Msg, error) {
		r := new(dns.Msg)
		r.SetRcode(q, dns.RcodeNameError)
		return r, nil
	})
	wrapped, e := wrapHandler(base, h)
	if e != nil {
		t.Fatal(e)
	}
	defer wrapped.Close()
	go func() {
		b := [][]byte{make([]byte, 65535)}
		sizes := make([]int, 1)
		for {
			n, e := client.Read(b, sizes, 0)
			if e != nil {
				return
			}
			if n > 0 {
				packet := append([]byte(nil), b[0][:sizes[0]]...)
				select {
				case base.incoming <- packet:
				case <-base.done:
					return
				}
			}
		}
	}()
	go func() {
		b := [][]byte{make([]byte, 65535)}
		sizes := make([]int, 1)
		for {
			if _, e := wrapped.Read(b, sizes, 0); e != nil {
				return
			}
		}
	}()
	for _, proto := range []string{"udp", "tcp"} {
		t.Run(proto, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, e := network.DialContext(ctx, proto, Address+":53")
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(5 * time.Second))
			wire := &dns.Conn{Conn: c}
			q := new(dns.Msg)
			q.SetQuestion("unknown.meldnet.internal.", dns.TypeA)
			if e = wire.WriteMsg(q); e != nil {
				t.Fatal(e)
			}
			r, e := wire.ReadMsg()
			if e != nil {
				t.Fatal(e)
			}
			if r.Id != q.Id || r.Rcode != dns.RcodeNameError {
				t.Fatal(r)
			}
		})
	}
}

func TestPrivateWithoutVPNNeverDials(t *testing.T) {
	p := policyFixture()
	p.VPNHandle = 0
	h := NewHandler(func() Policy { return p }, func(*dns.Msg, string, bool, uint64) (*dns.Msg, error) {
		t.Fatal("private query escaped VPN binding")
		return nil, nil
	})
	q := new(dns.Msg)
	q.SetQuestion("private.meldnet.internal.", dns.TypeA)
	w := new(writer)
	h.ServeDNS(w, q)
	if w.reply.Rcode != dns.RcodeServerFailure {
		t.Fatal(w.reply)
	}
}
func TestInvalidJSONClearsPartialPolicy(t *testing.T) {
	p, e := Parse(`{"primary":"10.77.0.1","pool":"10.77.0.0/24","upstreams":["192.168.1.1"],"networkHandle":42,"vpnHandle":"wrong"}`)
	if e == nil || p.Primary != "" || p.NetworkHandle != 0 {
		t.Fatal("partial invalid policy retained")
	}
}
