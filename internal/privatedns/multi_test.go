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

// The loopback aggregate's aliases must also be served on the primary VPN IP:
// mobile clients query that authority directly and never use the host aggregate.
func TestPrimaryProfileDNSAliases(t *testing.T) {
	hub := &Hub{slots: map[string]*Slot{}}
	slot := hub.Slot("default")
	if err := slot.Configure(testSettings()); err != nil {
		t.Fatal(err)
	}
	z := slot.primary.zone.current.Load()
	// Admit only loopback for this isolated transport test. Production admission
	// remains the enrolled VPN addresses populated by makeZone.
	if z.allowed[netip.MustParseAddr("127.0.0.1")] {
		t.Fatal("primary admits loopback")
	}
	if !z.allowed[netip.MustParseAddr("10.77.0.2")] {
		t.Fatal("enrolled member denied")
	}
	z.allowed[netip.MustParseAddr("127.0.0.1")] = true
	server, err := serve("127.0.0.1:0", &slot.primary.zone)
	if err != nil {
		t.Fatal(err)
	}
	defer server.close()
	addr := server.tcp.Listener.Addr().String()
	for _, transport := range []string{"udp", "tcp"} {
		for _, name := range []string{"imac.meldnet.internal.", "imac.default.meldnet.internal."} {
			r := testExchange(t, addr, transport, name, dns.TypeA)
			if len(r.Answer) != 1 || r.Answer[0].(*dns.A).A.String() != "10.77.0.2" {
				t.Fatalf("%s %s: %v", transport, name, r)
			}
			for _, typ := range []uint16{dns.TypeAAAA, dns.TypeHTTPS} {
				r := testExchange(t, addr, transport, name, typ)
				if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 0 || len(r.Ns) != 1 {
					t.Fatalf("missing authoritative NODATA: %v", r)
				}
			}
		}
		for _, name := range []string{"missing.default.meldnet.internal.", "imac.other.meldnet.internal."} {
			if r := testExchange(t, addr, transport, name, dns.TypeA); r.Rcode != dns.RcodeNameError {
				t.Fatalf("unknown alias: %v", r)
			}
		}
		if r := testExchange(t, addr, transport, "example.com.", dns.TypeA); r.Rcode != dns.RcodeRefused {
			t.Fatalf("public query accepted: %v", r)
		}
	}
	// A second membership update must replace both spellings, not retain aliases.
	settings := testSettings()
	settings.Records = settings.Records[:1]
	if err := slot.Configure(settings); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"imac.meldnet.internal.", "imac.default.meldnet.internal."} {
		if r := testExchange(t, addr, "udp", name, dns.TypeA); r.Rcode != dns.RcodeRefused {
			t.Fatalf("nonmember source admitted after removal: %v", r)
		}
		if _, ok := slot.primary.zone.current.Load().addresses[name]; ok {
			t.Fatal("removed member alias retained")
		}
	}
}

func TestPrimaryProfileQualifierValidation(t *testing.T) {
	for _, profile := range []string{"default", "work-office"} {
		settings := testSettings()
		settings.Profile = profile
		z, err := makeZone(settings)
		if err != nil {
			t.Fatal(err)
		}
		if z.reverse["2.0.77.10.in-addr.arpa."] != "imac.meldnet.internal." {
			t.Fatal("legacy PTR changed")
		}
	}
	for _, profile := range []string{"work.office", "../default", "default-", "DEFAULT"} {
		settings := testSettings()
		settings.Profile = profile
		if _, err := makeZone(settings); err == nil {
			t.Fatalf("unsafe profile %q", profile)
		}
	}
}
