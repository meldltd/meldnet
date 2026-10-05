package vpn

import (
	"context"
	"net/netip"
)

const tunnelMTU = 1280 // Conservative for IPv4 and IPv6 outer paths.

// routeSpec contains public routing metadata, never WireGuard keys.
type routeSpec struct {
	Prefix    string `json:"prefix"`
	Interface string `json:"interface"`
	Index     int    `json:"index"`
	Gateway   string `json:"gateway,omitempty"`
	Source    string `json:"source,omitempty"`
	LUID      uint64 `json:"luid,omitempty"` // Windows identity, prevents index-reuse cleanup.
}

type network interface {
	RouteTo(context.Context, netip.Addr) (routeSpec, error)
	Configure(context.Context, string, []string) (int, error)
	AddRoute(context.Context, routeSpec) error
	DeleteRoute(context.Context, routeSpec) error
	IsUp(string) (bool, error)
}

// Split defaults preserve the existing default route. Endpoint host routes
// ensure encrypted UDP does not recursively enter the tunnel.
func tunnelPrefixes(nets []string) []netip.Prefix {
	seen := map[netip.Prefix]bool{}
	var result []netip.Prefix
	for _, raw := range nets {
		p := netip.MustParsePrefix(raw).Masked()
		values := []netip.Prefix{p}
		if p.Bits() == 0 {
			if p.Addr().Is4() {
				values = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/1"), netip.MustParsePrefix("128.0.0.0/1")}
			} else {
				values = []netip.Prefix{netip.MustParsePrefix("::/1"), netip.MustParsePrefix("8000::/1")}
			}
		}
		for _, v := range values {
			if !seen[v] {
				seen[v] = true
				result = append(result, v)
			}
		}
	}
	return result
}
