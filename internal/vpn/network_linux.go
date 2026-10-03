package vpn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const routeProtocol = 186
const routePriority = 51820

type nativeNetwork struct{}

func newNetwork() network { return nativeNetwork{} }
func tunnelName() string  { return "meldnet" }

func handle(ctx context.Context) (*netlink.Handle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h, err := netlink.NewHandle(unix.NETLINK_ROUTE)
	if err != nil {
		return nil, err
	}
	timeout := 2 * time.Second
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < timeout {
		timeout = time.Until(deadline)
	}
	if err := h.SetSocketTimeout(timeout); err != nil {
		h.Close()
		return nil, err
	}
	return h, nil
}
func (nativeNetwork) RouteTo(ctx context.Context, ip netip.Addr) (routeSpec, error) {
	h, err := handle(ctx)
	if err != nil {
		return routeSpec{}, err
	}
	defer h.Close()
	routes, err := h.RouteGetWithOptions(net.IP(ip.AsSlice()), &netlink.RouteGetOptions{FIBMatch: true})
	if err != nil {
		return routeSpec{}, err
	}
	for _, r := range routes {
		if r.Type != unix.RTN_UNICAST && r.Type != unix.RTN_LOCAL {
			continue
		}
		link, err := h.LinkByIndex(r.LinkIndex)
		if err != nil {
			continue
		}
		if link.Attrs().Name == tunnelName() {
			continue
		}
		prefix := "0.0.0.0/0"
		if ip.Is6() {
			prefix = "::/0"
		}
		if r.Dst != nil {
			prefix = r.Dst.String()
		}
		s := routeSpec{Prefix: prefix, Interface: link.Attrs().Name, Index: r.LinkIndex}
		if len(r.Gw) > 0 {
			s.Gateway = r.Gw.String()
		}
		if len(r.Src) > 0 {
			s.Source = r.Src.String()
		}
		return s, nil
	}
	return routeSpec{}, errors.New("no unicast underlay route")
}
func (nativeNetwork) Configure(ctx context.Context, name string, addresses []string) (int, error) {
	h, err := handle(ctx)
	if err != nil {
		return 0, err
	}
	defer h.Close()
	link, err := h.LinkByName(name)
	if err != nil {
		return 0, err
	}
	for _, raw := range addresses {
		addr, err := netlink.ParseAddr(raw)
		if err != nil {
			return 0, err
		}
		// Only AllowedIPs create routes, not the assigned address's entire subnet.
		addr.Flags = unix.IFA_F_NOPREFIXROUTE | unix.IFA_F_NODAD
		if err := h.AddrAdd(link, addr); err != nil {
			return 0, err
		}
	}
	if err := h.LinkSetUp(link); err != nil {
		return 0, err
	}
	return link.Attrs().Index, nil
}
func linuxRoute(r routeSpec) netlink.Route {
	p := netip.MustParsePrefix(r.Prefix)
	rt := netlink.Route{LinkIndex: r.Index, Dst: &net.IPNet{IP: net.IP(p.Addr().AsSlice()), Mask: net.CIDRMask(p.Bits(), p.Addr().BitLen())}, Table: unix.RT_TABLE_MAIN, Protocol: routeProtocol, Priority: routePriority, Scope: netlink.SCOPE_LINK, Type: unix.RTN_UNICAST}
	if r.Gateway != "" {
		rt.Gw = net.ParseIP(r.Gateway)
		rt.Scope = netlink.SCOPE_UNIVERSE
	}
	if r.Source != "" {
		rt.Src = net.ParseIP(r.Source)
	}
	return rt
}
func (nativeNetwork) AddRoute(ctx context.Context, r routeSpec) error {
	h, err := handle(ctx)
	if err != nil {
		return err
	}
	defer h.Close()
	link, err := h.LinkByIndex(r.Index)
	if err != nil {
		return err
	}
	if link.Attrs().Name != r.Interface {
		return errors.New("interface identity changed")
	}
	rt := linuxRoute(r)
	existing, err := h.RouteListFiltered(netlink.FAMILY_ALL, &rt, netlink.RT_FILTER_DST|netlink.RT_FILTER_TABLE)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return fmt.Errorf("route %s already exists; existing routes are not replaced", r.Prefix)
	}
	return h.RouteAdd(&rt)
}
func (nativeNetwork) DeleteRoute(ctx context.Context, r routeSpec) error {
	h, err := handle(ctx)
	if err != nil {
		return err
	}
	defer h.Close()
	link, err := h.LinkByIndex(r.Index)
	if err != nil {
		var absent netlink.LinkNotFoundError
		if errors.As(err, &absent) {
			return nil
		}
		return err
	}
	if link.Attrs().Name != r.Interface {
		return nil
	}
	rt := linuxRoute(r)
	routes, err := h.RouteListFiltered(netlink.FAMILY_ALL, &rt, netlink.RT_FILTER_DST|netlink.RT_FILTER_TABLE)
	if err != nil {
		return err
	}
	for _, actual := range routes {
		// Delete only a route bearing our marker and exact saved next hop. If an
		// administrator replaced it, leave the replacement untouched.
		if actual.Protocol == routeProtocol && actual.Priority == routePriority && actual.LinkIndex == r.Index && actual.Gw.Equal(rt.Gw) && actual.Src.Equal(rt.Src) {
			err = h.RouteDel(&actual)
			if errors.Is(err, unix.ESRCH) {
				return nil
			}
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
