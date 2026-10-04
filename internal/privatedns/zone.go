// Package privatedns owns private DNS and reversible OS resolver integration.
package privatedns

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/miekg/dns"
)

const Domain = "meldnet.internal"
const TTL = 30

type Record struct {
	Name      string `json:"name"`
	IP        string `json:"ip"`
	PublicKey string `json:"public_key"`
}
type Settings struct {
	KnownPools []string            // retain private reverse-zone boundaries while another profile is active
	Networks   map[string]Settings // local-only aggregate, never accepted over the control API

	Server  string
	Pool    string
	Primary bool
	Records []Record
}
type Status struct {
	Domain string `json:"domain,omitempty"`
	Server string `json:"server,omitempty"`
	Active bool   `json:"active"`
	Mode   string `json:"mode,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Hostnames preserves ordinary names, and provides deterministic DNS-safe names
// for legacy node names that contain dots/underscores or collide ignoring case.
func Hostnames(records []Record) map[string]string {
	counts := map[string]int{}
	for _, r := range records {
		counts[strings.ToLower(r.Name)]++
	}
	out := map[string]string{}
	for _, r := range records {
		label := strings.ToLower(r.Name)
		if !validLabel(label) || counts[label] > 1 {
			h := sha256.Sum256([]byte(r.PublicKey))
			label = "node-" + hex.EncodeToString(h[:8])
		}
		out[r.PublicKey] = label + "." + Domain
	}
	// A manually chosen name may equal an automatically generated label. Use a
	// public-key-derived suffix for the entire colliding group in that case.
	groups := map[string][]Record{}
	for _, r := range records {
		groups[out[r.PublicKey]] = append(groups[out[r.PublicKey]], r)
	}
	for _, group := range groups {
		if len(group) > 1 {
			for _, r := range group {
				h := sha256.Sum256([]byte(r.PublicKey))
				out[r.PublicKey] = "node-" + hex.EncodeToString(h[:24]) + "." + Domain
			}
		}
	}
	return out
}
func validLabel(s string) bool {
	if len(s) == 0 || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

type zone struct {
	addresses map[string]netip.Addr
	reverse   map[string]string
	allowed   map[netip.Addr]bool
	pool      netip.Prefix
	pools     []netip.Prefix
	ns        string
}
type authority struct{ current atomic.Pointer[zone] }

func makeZone(s Settings) (*zone, error) {
	if len(s.Networks) > 0 {
		z, err := mergedZone(s.Networks)
		if err != nil {
			return nil, err
		}
		for _, raw := range s.KnownPools {
			p, e := netip.ParsePrefix(raw)
			if e != nil || !p.Addr().Is4() || !p.Addr().IsPrivate() || p != p.Masked() || p.Bits() < 16 || p.Bits() > 28 {
				return nil, errors.New("invalid private DNS range")
			}
			z.pools = append(z.pools, p)
		}
		return z, nil
	}
	p, e := netip.ParsePrefix(s.Pool)
	server, se := netip.ParseAddr(s.Server)
	if e != nil || se != nil || !p.Addr().Is4() || !p.Addr().IsPrivate() || p != p.Masked() || p.Bits() < 16 || p.Bits() > 28 || !p.Contains(server) || server != p.Addr().Next() || len(s.Records) > 129 {
		return nil, errors.New("invalid private DNS network")
	}
	z := &zone{addresses: map[string]netip.Addr{}, reverse: map[string]string{}, allowed: map[netip.Addr]bool{}, pool: p}
	names := Hostnames(s.Records)
	for _, r := range s.Records {
		ip, e := netip.ParseAddr(r.IP)
		if e != nil || !p.Contains(ip) || !ip.Is4() {
			return nil, errors.New("invalid private DNS member address")
		}
		name := dns.Fqdn(names[r.PublicKey])
		if _, ok := z.addresses[name]; ok {
			return nil, errors.New("duplicate private DNS hostname")
		}
		if z.allowed[ip] {
			return nil, errors.New("duplicate private DNS address")
		}
		z.addresses[name] = ip
		z.allowed[ip] = true
		reverse, _ := dns.ReverseAddr(ip.String())
		z.reverse[reverse] = name
		if ip == server {
			z.ns = name
		}
	}
	if z.ns == "" {
		return nil, errors.New("private DNS primary is missing")
	}
	return z, nil
}
func (z *zone) private(name string) bool {
	name = strings.ToLower(dns.Fqdn(name))
	if dns.IsSubDomain(Domain+".", name) {
		return true
	}
	if !strings.HasSuffix(name, ".in-addr.arpa.") {
		return false
	}
	parts := strings.Split(strings.TrimSuffix(name, ".in-addr.arpa."), ".")
	if len(parts) != 4 {
		return false
	}
	ip, e := netip.ParseAddr(parts[3] + "." + parts[2] + "." + parts[1] + "." + parts[0])
	if e != nil {
		return false
	}
	for _, pool := range z.networkPools() {
		if pool.Contains(ip) {
			return true
		}
	}
	return false
}
func (z *zone) soa() dns.RR {
	return &dns.SOA{Hdr: dns.RR_Header{Name: Domain + ".", Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: 5}, Ns: z.ns, Mbox: "hostmaster." + Domain + ".", Serial: 1, Refresh: 30, Retry: 5, Expire: 300, Minttl: 5}
}
func (a *authority) ServeDNS(w dns.ResponseWriter, q *dns.Msg) {
	z := a.current.Load()
	host, _, e := net.SplitHostPort(w.RemoteAddr().String())
	ip, ie := netip.ParseAddr(host)
	if z == nil || e != nil || ie != nil || !z.allowed[ip.Unmap()] {
		replyCode(w, q, dns.RcodeRefused)
		return
	}
	a.answer(w, q)
}
func (a *authority) answer(w dns.ResponseWriter, q *dns.Msg) {
	z := a.current.Load()
	if z == nil {
		replyCode(w, q, dns.RcodeServerFailure)
		return
	}
	if q.Opcode != dns.OpcodeQuery || len(q.Question) != 1 {
		replyCode(w, q, dns.RcodeFormatError)
		return
	}
	question := q.Question[0]
	name := strings.ToLower(dns.Fqdn(question.Name))
	m := new(dns.Msg)
	m.SetReply(q)
	if question.Qclass != dns.ClassINET || !z.private(name) {
		m.Rcode = dns.RcodeRefused
		writeReply(w, q, m)
		return
	}
	m.Authoritative = true
	hdr := dns.RR_Header{Name: question.Name, Class: dns.ClassINET, Ttl: TTL}
	if name == Domain+"." {
		switch question.Qtype {
		case dns.TypeSOA:
			m.Answer = []dns.RR{z.soa()}
		case dns.TypeNS:
			hdr.Rrtype = dns.TypeNS
			m.Answer = []dns.RR{&dns.NS{Hdr: hdr, Ns: z.ns}}
		}
	} else if ip, ok := z.addresses[name]; ok {
		if question.Qtype == dns.TypeA {
			hdr.Rrtype = dns.TypeA
			m.Answer = []dns.RR{&dns.A{Hdr: hdr, A: net.IP(ip.AsSlice())}}
		}
	} else if hostname, ok := z.reverse[name]; ok {
		if question.Qtype == dns.TypePTR {
			hdr.Rrtype = dns.TypePTR
			m.Answer = []dns.RR{&dns.PTR{Hdr: hdr, Ptr: hostname}}
		}
	} else {
		m.Rcode = dns.RcodeNameError
	}
	if len(m.Answer) == 0 {
		soa := z.soa()
		if strings.HasSuffix(name, ".in-addr.arpa.") {
			for _, p := range z.networkPools() {
				if strings.HasSuffix(name, reverseDomain(p)+".") {
					soa.Header().Name = reverseDomain(p) + "."
					break
				}
			}
		}
		m.Ns = []dns.RR{soa}
	}
	writeReply(w, q, m)
}
func replyCode(w dns.ResponseWriter, q *dns.Msg, code int) {
	m := new(dns.Msg)
	m.SetRcode(q, code)
	writeReply(w, q, m)
}
func writeReply(w dns.ResponseWriter, q, m *dns.Msg) {
	m.Compress = true
	if _, ok := w.RemoteAddr().(*net.UDPAddr); ok {
		size := 512
		if o := q.IsEdns0(); o != nil {
			size = max(512, min(int(o.UDPSize()), 1232))
			if existing := m.IsEdns0(); existing != nil {
				existing.SetUDPSize(uint16(size))
			} else {
				m.SetEdns0(uint16(size), false)
			}
		}
		m.Truncate(size)
	}
	_ = w.WriteMsg(m)
}
func sortedRecords(in []Record) []Record {
	out := append([]Record{}, in...)
	sort.Slice(out, func(i, j int) bool { return out[i].PublicKey < out[j].PublicKey })
	return out
}

// Reverse routing follows the containing octet boundary; the authority still
// answers only addresses inside the exact configured pool.
func reverseDomain(p netip.Prefix) string {
	parts := strings.Split(p.Addr().String(), ".")
	var reverse []string
	for i := p.Bits()/8 - 1; i >= 0; i-- {
		reverse = append(reverse, parts[i])
	}
	return strings.Join(reverse, ".") + ".in-addr.arpa"
}

func (z *zone) networkPools() []netip.Prefix {
	if len(z.pools) > 0 {
		return z.pools
	}
	return []netip.Prefix{z.pool}
}
func (z *zone) reverseDomains() []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range z.networkPools() {
		d := reverseDomain(p)
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// Names qualified by profile are always unique. Bare legacy names are retained
// only when exactly one connected network advertises them; ambiguity is NXDOMAIN.
func mergedZone(networks map[string]Settings) (*zone, error) {
	z := &zone{addresses: map[string]netip.Addr{}, reverse: map[string]string{}, allowed: map[netip.Addr]bool{netip.MustParseAddr("127.0.0.1"): true}, ns: "localhost."}
	bare := map[string][]netip.Addr{}
	ids := []string{}
	for id := range networks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if !validLabel(id) {
			return nil, errors.New("invalid DNS network ID")
		}
		part, err := makeZone(networks[id])
		if err != nil {
			return nil, err
		}
		z.pools = append(z.pools, part.pool)
		for name, ip := range part.addresses {
			qualified := strings.TrimSuffix(name, "."+Domain+".") + "." + id + "." + Domain + "."
			z.addresses[qualified] = ip
			bare[name] = append(bare[name], ip)
			reverse, _ := dns.ReverseAddr(ip.String())
			z.reverse[reverse] = qualified
		}
	}
	if len(z.pools) == 0 {
		return nil, errors.New("empty DNS networks")
	}
	z.pool = z.pools[0]
	for name, ips := range bare {
		if len(ips) == 1 {
			z.addresses[name] = ips[0]
			reverse, _ := dns.ReverseAddr(ips[0].String())
			z.reverse[reverse] = name
		}
	}
	return z, nil
}
