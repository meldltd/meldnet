package vpn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

const windowsRouteMetric = 51820

type nativeNetwork struct{}

func newNetwork() network { return nativeNetwork{} }
func tunnelName() string  { return "meldnet" }

func (nativeNetwork) RouteTo(ctx context.Context, ip netip.Addr) (routeSpec, error) {
	if err := ctx.Err(); err != nil {
		return routeSpec{}, err
	}
	family := winipcfg.AddressFamily(windows.AF_INET)
	if ip.Is6() {
		family = windows.AF_INET6
	}
	routes, err := winipcfg.GetIPForwardTable2(family)
	if err != nil {
		return routeSpec{}, err
	}
	var best *winipcfg.MibIPforwardRow2
	var cost uint64
	var name string
	for i := range routes {
		r := &routes[i]
		if !r.DestinationPrefix.Prefix().Contains(ip) || r.Loopback {
			continue
		}
		iface, err := r.InterfaceLUID.Interface()
		if err != nil || iface.OperStatus != winipcfg.IfOperStatusUp || strings.HasPrefix(iface.Alias(), "meldnet") {
			continue
		}
		ipif, err := r.InterfaceLUID.IPInterface(family)
		if err != nil {
			continue
		}
		metric := uint64(r.Metric) + uint64(ipif.Metric)
		if best == nil || r.DestinationPrefix.Prefix().Bits() > best.DestinationPrefix.Prefix().Bits() || r.DestinationPrefix.Prefix().Bits() == best.DestinationPrefix.Prefix().Bits() && metric < cost {
			best, cost, name = r, metric, iface.Alias()
		}
	}
	if best == nil {
		return routeSpec{}, errors.New("no unicast underlay route")
	}
	gateway := best.NextHop.Addr()
	r := routeSpec{Prefix: best.DestinationPrefix.Prefix().String(), Interface: name, Index: int(best.InterfaceIndex), LUID: uint64(best.InterfaceLUID)}
	if gateway.IsValid() && !gateway.IsUnspecified() {
		r.Gateway = gateway.String()
	}
	return r, nil
}

func (nativeNetwork) Configure(ctx context.Context, name string, addresses []string) (int, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return 0, err
	}
	luid, err := winipcfg.LUIDFromIndex(uint32(iface.Index))
	if err != nil {
		return 0, err
	}
	for _, raw := range addresses {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			return 0, err
		}
		// Host-sized assignments avoid Windows' implicit subnet routes. AllowedIPs
		// below are the only networks this daemon deliberately routes.
		if err := luid.AddIPAddress(netip.PrefixFrom(p.Addr(), p.Addr().BitLen())); err != nil {
			return 0, err
		}
	}
	for _, family := range []winipcfg.AddressFamily{windows.AF_INET, windows.AF_INET6} {
		row, err := luid.IPInterface(family)
		if err != nil {
			return 0, err
		}
		row.NLMTU = tunnelMTU
		row.UseAutomaticMetric = false
		row.Metric = 0
		row.DadTransmits = 0
		row.RouterDiscoveryBehavior = winipcfg.RouterDiscoveryDisabled
		if err := row.Set(); err != nil {
			return 0, err
		}
	}
	return iface.Index, nil
}

func windowsRoute(r routeSpec) (*winipcfg.MibIPforwardRow2, error) {
	luid, err := winipcfg.LUIDFromIndex(uint32(r.Index))
	if err != nil {
		return nil, err
	}
	iface, err := luid.Interface()
	if err != nil {
		return nil, err
	}
	if iface.Alias() != r.Interface || r.LUID != 0 && uint64(luid) != r.LUID {
		return nil, errors.New("interface identity changed")
	}
	p, err := netip.ParsePrefix(r.Prefix)
	if err != nil {
		return nil, err
	}
	next := netip.IPv4Unspecified()
	if p.Addr().Is6() {
		next = netip.IPv6Unspecified()
	}
	if r.Gateway != "" {
		next, err = netip.ParseAddr(r.Gateway)
		if err != nil {
			return nil, err
		}
	}
	row := &winipcfg.MibIPforwardRow2{}
	row.Init()
	row.InterfaceLUID = luid
	row.InterfaceIndex = uint32(r.Index)
	if err := row.DestinationPrefix.SetPrefix(p); err != nil {
		return nil, err
	}
	if err := row.NextHop.SetAddr(next); err != nil {
		return nil, err
	}
	row.Metric = windowsRouteMetric
	row.Protocol = winipcfg.RouteProtocolNetMgmt
	return row, nil
}

func (nativeNetwork) AddRoute(ctx context.Context, r routeSpec) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	row, err := windowsRoute(r)
	if err != nil {
		return err
	}
	family := winipcfg.AddressFamily(windows.AF_INET)
	if row.DestinationPrefix.Prefix().Addr().Is6() {
		family = windows.AF_INET6
	}
	routes, err := winipcfg.GetIPForwardTable2(family)
	if err != nil {
		return err
	}
	for _, existing := range routes {
		if existing.DestinationPrefix.Prefix() == row.DestinationPrefix.Prefix() {
			return fmt.Errorf("route %s already exists; existing routes are not replaced", r.Prefix)
		}
	}
	return row.Create()
}

func (nativeNetwork) DeleteRoute(ctx context.Context, r routeSpec) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	routes, err := winipcfg.GetIPForwardTable2(windows.AF_UNSPEC)
	if err != nil {
		return err
	}
	for _, actual := range routes {
		if actual.DestinationPrefix.Prefix().String() != r.Prefix || int(actual.InterfaceIndex) != r.Index || actual.Metric != windowsRouteMetric || actual.Protocol != winipcfg.RouteProtocolNetMgmt {
			continue
		}
		if r.LUID == 0 || uint64(actual.InterfaceLUID) != r.LUID {
			continue
		}
		iface, err := actual.InterfaceLUID.Interface()
		if err != nil {
			return err
		}
		if iface.Alias() != r.Interface {
			continue
		}
		next := actual.NextHop.Addr()
		if r.Gateway == "" && !next.IsUnspecified() || r.Gateway != "" && next.String() != r.Gateway {
			continue
		}
		if err := actual.Delete(); err != nil && !errors.Is(err, windows.ERROR_NOT_FOUND) {
			return err
		}
	}
	return nil
}

func (nativeNetwork) IsUp(name string) (bool, error) {
	i, err := net.InterfaceByName(name)
	if err != nil {
		return false, err
	}
	return i.Flags&net.FlagUp != 0, nil
}
