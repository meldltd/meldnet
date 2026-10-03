package privatedns

import (
	"net"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/miekg/dns"
)

func testSettings() Settings {
	return Settings{Server: "10.77.0.1", Pool: "10.77.0.0/24", Primary: true, Records: []Record{{Name: "primary", IP: "10.77.0.1", PublicKey: "primary-key"}, {Name: "imac", IP: "10.77.0.2", PublicKey: "client-key"}}}
}
func testAuthority(t *testing.T) *authority {
	t.Helper()
	a := &authority{}
	z, e := makeZone(testSettings())
	if e != nil {
		t.Fatal(e)
	}
	a.current.Store(z)
	return a
}
func testExchange(t *testing.T, address, network, name string, kind uint16) *dns.Msg {
	t.Helper()
	q := new(dns.Msg)
	q.SetQuestion(name, kind)
	q.SetEdns0(1232, false)
	r, _, e := (&dns.Client{Net: network}).Exchange(q, address)
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for _, rr := range r.Extra {
		if _, ok := rr.(*dns.OPT); ok {
			count++
		}
	}
	if count > 1 {
		t.Fatal("duplicate EDNS OPT records")
	}
	return r
}
func TestAuthoritativeDNS(t *testing.T) {
	a := testAuthority(t)
	a.current.Load().allowed[netip.MustParseAddr("127.0.0.1")] = true
	s, e := serve("127.0.0.1:0", a)
	if e != nil {
		t.Fatal(e)
	}
	defer s.close()
	address := s.tcp.Listener.Addr().String()
	for _, network := range []string{"udp", "tcp"} {
		r := testExchange(t, address, network, "IMAC.meldnet.internal.", dns.TypeA)
		if !r.Authoritative || r.Rcode != dns.RcodeSuccess || len(r.Answer) != 1 || r.Answer[0].(*dns.A).A.String() != "10.77.0.2" {
			t.Fatalf("bad answer: %v", r)
		}
		r = testExchange(t, address, network, "imac.meldnet.internal.", dns.TypeAAAA)
		if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 0 || len(r.Ns) != 1 {
			t.Fatal("existing name should have NODATA for IPv6")
		}
		r = testExchange(t, address, network, "missing.meldnet.internal.", dns.TypeA)
		if r.Rcode != dns.RcodeNameError || len(r.Ns) != 1 {
			t.Fatal("missing name should return NXDOMAIN with negative TTL")
		}
		r = testExchange(t, address, network, "2.0.77.10.in-addr.arpa.", dns.TypePTR)
		if len(r.Answer) != 1 || r.Answer[0].(*dns.PTR).Ptr != "imac.meldnet.internal." {
			t.Fatal("missing reverse record")
		}
		r = testExchange(t, address, network, "example.com.", dns.TypeA)
		if r.Rcode != dns.RcodeRefused || r.RecursionAvailable {
			t.Fatal("primary must not offer recursion")
		}
	}
	settings := testSettings()
	settings.Records = settings.Records[:1]
	z, e := makeZone(settings)
	if e != nil {
		t.Fatal(e)
	}
	z.allowed[netip.MustParseAddr("127.0.0.1")] = true
	a.current.Store(z)
	if r := testExchange(t, address, "udp", "imac.meldnet.internal.", dns.TypeA); r.Rcode != dns.RcodeNameError {
		t.Fatal("revoked record still published")
	}
	// Unknown source addresses cannot query even public-looking private records.
	z2, _ := makeZone(testSettings())
	a.current.Store(z2)
	if r := testExchange(t, address, "tcp", "primary.meldnet.internal.", dns.TypeA); r.Rcode != dns.RcodeRefused {
		t.Fatal("unregistered source accepted")
	}
}
func TestProxyRoutesPrivateNamesWithoutLeakage(t *testing.T) {
	a := testAuthority(t)
	private, e := serve("127.0.0.1:0", dns.HandlerFunc(a.answer))
	if e != nil {
		t.Fatal(e)
	}
	defer private.close()
	var publicCalls atomic.Int32
	public, e := serve("127.0.0.1:0", dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		publicCalls.Add(1)
		m := new(dns.Msg)
		m.SetReply(q)
		m.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.IPv4(192, 0, 2, 10)}}
		writeReply(w, q, m)
	}))
	if e != nil {
		t.Fatal(e)
	}
	defer public.close()
	f := &forwarder{zone: a, privateServer: private.tcp.Listener.Addr().String(), upstreams: []string{public.tcp.Listener.Addr().String()}}
	proxy, e := serve("127.0.0.1:0", f)
	if e != nil {
		t.Fatal(e)
	}
	defer proxy.close()
	address := proxy.tcp.Listener.Addr().String()
	for _, network := range []string{"udp", "tcp"} {
		r := testExchange(t, address, network, "imac.meldnet.internal.", dns.TypeA)
		if len(r.Answer) != 1 || r.Answer[0].(*dns.A).A.String() != "10.77.0.2" {
			t.Fatal("private resolution failed")
		}
		r = testExchange(t, address, network, "missing.meldnet.internal.", dns.TypeA)
		if r.Rcode != dns.RcodeNameError {
			t.Fatal("private negative response lost")
		}
	}
	f.suspended.Store(true)
	if r := testExchange(t, address, "udp", "imac.meldnet.internal.", dns.TypeA); r.Rcode != dns.RcodeServerFailure {
		t.Fatal("disconnected proxy forwarded private lookup")
	}
	if publicCalls.Load() != 0 {
		t.Fatal("private query leaked to public upstream")
	}
	r := testExchange(t, address, "udp", "example.com.", dns.TypeA)
	if len(r.Answer) != 1 || r.Answer[0].(*dns.A).A.String() != "192.0.2.10" || publicCalls.Load() != 1 {
		t.Fatal("public DNS not preserved")
	}
}
func TestHostnamesAreValidAndUnambiguous(t *testing.T) {
	records := []Record{{Name: "imac", PublicKey: "a"}, {Name: "weird_name.", PublicKey: "b"}, {Name: "FOO", PublicKey: "c"}, {Name: "foo", PublicKey: "d"}}
	names := Hostnames(records)
	if names["a"] != "imac.meldnet.internal" {
		t.Fatal("ordinary device name changed")
	}
	seen := map[string]bool{}
	for _, name := range names {
		label := strings.TrimSuffix(name, "."+Domain)
		if !validLabel(label) || seen[name] {
			t.Fatal("invalid or duplicate hostname")
		}
		seen[name] = true
	}
	settings := testSettings()
	settings.Server = "8.8.8.8"
	if _, e := makeZone(settings); e == nil {
		t.Fatal("accepted external DNS server")
	}
}
