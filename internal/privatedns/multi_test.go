package privatedns

import (
	"github.com/miekg/dns"
	"net/netip"
	"testing"
)

func secondSettings() Settings {
	return Settings{Server: "10.88.0.1", Pool: "10.88.0.0/24", Records: []Record{{Name: "primary", IP: "10.88.0.1", PublicKey: "second-primary"}, {Name: "imac", IP: "10.88.0.2", PublicKey: "second-client"}}}
}
func TestMultiNetworkDNSIsolation(t *testing.T) {
	first, second := testSettings(), secondSettings()
	z, err := makeZone(Settings{Networks: map[string]Settings{"work": first, "home": second}})
	if err != nil {
		t.Fatal(err)
	}
	a := &authority{}
	a.current.Store(z)
	server, err := serve("127.0.0.1:0", a)
	if err != nil {
		t.Fatal(err)
	}
	defer server.close()
	addr := server.tcp.Listener.Addr().String()
	for _, transport := range []string{"udp", "tcp"} {
		for name, want := range map[string]string{"imac.work.meldnet.internal.": "10.77.0.2", "imac.home.meldnet.internal.": "10.88.0.2"} {
			r := testExchange(t, addr, transport, name, dns.TypeA)
			if len(r.Answer) != 1 || r.Answer[0].(*dns.A).A.String() != want {
				t.Fatalf("wrong network: %v", r)
			}
		}
		if r := testExchange(t, addr, transport, "imac.meldnet.internal.", dns.TypeA); r.Rcode != dns.RcodeNameError {
			t.Fatal("ambiguous hostname guessed a network")
		}
	}
	next, err := makeZone(Settings{Networks: map[string]Settings{"home": second}, KnownPools: []string{first.Pool}})
	if err != nil {
		t.Fatal(err)
	}
	a.current.Store(next)
	if r := testExchange(t, addr, "udp", "imac.work.meldnet.internal.", dns.TypeA); r.Rcode != dns.RcodeNameError {
		t.Fatal("disconnected network records retained")
	}
	if !next.private("2.0.77.10.in-addr.arpa.") {
		t.Fatal("disconnected private reverse query could leak to upstream")
	}
	if r := testExchange(t, addr, "udp", "2.0.77.10.in-addr.arpa.", dns.TypePTR); r.Rcode != dns.RcodeNameError {
		t.Fatal("disconnected reverse record retained")
	}
	if next.addresses["imac.home.meldnet.internal."] != netip.MustParseAddr("10.88.0.2") {
		t.Fatal("remaining network lost")
	}
	if !next.addresses["imac.meldnet.internal."].IsValid() {
		t.Fatal("unambiguous legacy hostname missing")
	}
}
