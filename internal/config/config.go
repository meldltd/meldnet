// Package config defines the presentation-independent configuration contract.
package config

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

const Version = 1

type Peer struct {
	Name       string   `json:"name"`
	PublicKey  string   `json:"public_key"`
	Endpoint   string   `json:"endpoint,omitempty"`
	AllowedIPs []string `json:"allowed_ips"`
	Keepalive  int      `json:"keepalive"`
}

type Settings struct {
	Name       string   `json:"name"`
	Addresses  []string `json:"addresses"`
	ListenPort int      `json:"listen_port"`
	Peers      []Peer   `json:"peers"`
}

// Public is the only configuration representation sent to clients.
type Public struct {
	Revision  uint64   `json:"revision"`
	PublicKey string   `json:"public_key"`
	Settings  Settings `json:"settings"`
}

type Node struct {
	Version    int      `json:"version"`
	Revision   uint64   `json:"revision"`
	PrivateKey string   `json:"private_key"`
	Settings   Settings `json:"settings"`
}

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,31}$`)
var hostPattern = regexp.MustCompile(`^[a-zA-Z0-9.-]+$`)

func New(s Settings) (*Node, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	key[0] &= 248
	key[31] &= 127
	key[31] |= 64
	return &Node{Version: Version, Revision: 1, PrivateKey: base64.StdEncoding.EncodeToString(key), Settings: s}, nil
}

func (n Node) Public() Public {
	raw, _ := base64.StdEncoding.DecodeString(n.PrivateKey)
	key, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return Public{}
	} // Invalid persisted keys are rejected by Load.
	return Public{Revision: n.Revision, PublicKey: base64.StdEncoding.EncodeToString(key.PublicKey().Bytes()), Settings: n.Settings}
}

func ValidateKey(value string) error {
	b, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(b) != 32 || base64.StdEncoding.EncodeToString(b) != value {
		return errors.New("key must be a canonical base64-encoded 32-byte WireGuard key")
	}
	var nonzero byte
	for _, v := range b {
		nonzero |= v
	}
	if nonzero == 0 {
		return errors.New("key must not be zero")
	}
	return nil
}

func (s Settings) Validate() error {
	if !namePattern.MatchString(s.Name) {
		return errors.New("name must be 1–32 letters, digits, dots, underscores, or hyphens, starting with a letter or digit")
	}
	if len(s.Addresses) == 0 || len(s.Addresses) > 8 {
		return errors.New("provide 1–8 interface addresses in CIDR notation")
	}
	seen := map[string]bool{}
	for _, a := range s.Addresses {
		p, err := netip.ParsePrefix(a)
		if err != nil || p.Addr().Is4In6() || p.Addr().IsUnspecified() || p.Addr().IsMulticast() {
			return errors.New("interface addresses must be IPv4/IPv6 host addresses with a prefix length")
		}
		if seen[p.String()] {
			return errors.New("duplicate interface address")
		}
		seen[p.String()] = true
	}
	if s.ListenPort < 0 || s.ListenPort > 65535 {
		return errors.New("listen port must be 0–65535 (0 selects automatically)")
	}
	if len(s.Peers) > 128 {
		return errors.New("maximum 128 peers")
	}
	keys, names := map[string]bool{}, map[string]bool{}
	type route struct {
		prefix netip.Prefix
		peer   int
	}
	var routes []route
	for i, p := range s.Peers {
		if !namePattern.MatchString(p.Name) {
			return fmt.Errorf("peer %d: invalid name", i+1)
		}
		if names[p.Name] {
			return errors.New("peer names must be unique")
		}
		names[p.Name] = true
		if err := ValidateKey(p.PublicKey); err != nil {
			return fmt.Errorf("peer %s: %w", p.Name, err)
		}
		if keys[p.PublicKey] {
			return errors.New("peer public keys must be unique")
		}
		keys[p.PublicKey] = true
		if p.Endpoint != "" {
			host, port, err := net.SplitHostPort(p.Endpoint)
			pn, pe := strconv.Atoi(port)
			if err != nil || pe != nil || pn < 1 || pn > 65535 || !validHost(host) {
				return fmt.Errorf("peer %s: endpoint must be host:port or [IPv6]:port", p.Name)
			}
		}
		if p.Keepalive < 0 || p.Keepalive > 65535 {
			return fmt.Errorf("peer %s: keepalive must be 0–65535 seconds", p.Name)
		}
		if len(p.AllowedIPs) == 0 || len(p.AllowedIPs) > 64 {
			return fmt.Errorf("peer %s: provide 1–64 allowed IP prefixes", p.Name)
		}
		for _, a := range p.AllowedIPs {
			prefix, err := netip.ParsePrefix(a)
			if err != nil || prefix.Addr().Is4In6() || prefix != prefix.Masked() {
				return fmt.Errorf("peer %s: allowed IPs must use network CIDR notation (for a host use /32 or /128)", p.Name)
			}
			for _, r := range routes {
				if r.prefix == prefix || (r.peer != i && r.prefix.Overlaps(prefix)) {
					return errors.New("allowed IP prefixes must not overlap between peers or be duplicated")
				}
			}
			routes = append(routes, route{prefix, i})
		}
	}
	return nil
}

func validHost(host string) bool {
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Zone() == "" && !ip.IsUnspecified() && !ip.IsMulticast()
	}
	if len(host) == 0 || len(host) > 253 || !hostPattern.MatchString(host) {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
	}
	return true
}

func (n Node) Validate() error {
	if n.Version != Version || n.Revision == 0 {
		return errors.New("unsupported configuration version or revision")
	}
	if err := ValidateKey(n.PrivateKey); err != nil {
		return errors.New("invalid persisted private key")
	}
	if err := n.Settings.Validate(); err != nil {
		return err
	}
	for _, p := range n.Settings.Peers {
		if p.PublicKey == n.Public().PublicKey {
			return errors.New("a node cannot peer with itself")
		}
	}
	return nil
}
