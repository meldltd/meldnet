package vpn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

type nativeNetwork struct{}

func newNetwork() network { return nativeNetwork{} }
func tunnelName() string  { return "utun" }

// Layouts match Apple's ifaliasreq and in6_aliasreq in the macOS SDK. Both
// supported Darwin architectures use the same 64-bit ABI (64 and 128 bytes).
type alias4 struct {
	Name            [16]byte
	Addr, Dst, Mask unix.RawSockaddrInet4
}
type lifetime6 struct {
	Expire, Preferred        int64
	Valid, PreferredLifetime uint32
}
type alias6 struct {
	Name            [16]byte
	Addr, Dst, Mask unix.RawSockaddrInet6
	Flags           int32
	Lifetime        lifetime6
}
type flagsRequest struct {
	Name    [16]byte
	Flags   uint16
	Padding [14]byte
}

func ioctl(fd int, request uintptr, arg unsafe.Pointer) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), request, uintptr(arg))
	if errno != 0 {
		return errno
	}
	return nil
}
func (nativeNetwork) Configure(ctx context.Context, name string, addresses []string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	for _, raw := range addresses {
		ip := netip.MustParsePrefix(raw).Addr()
		family := unix.AF_INET
		if ip.Is6() {
			family = unix.AF_INET6
		}
		fd, err := unix.Socket(family, unix.SOCK_DGRAM, 0)
		if err != nil {
			return 0, err
		}
		unix.CloseOnExec(fd)
		// Assign point-to-point host addresses. Routes are explicitly derived from
		// AllowedIPs; assigning /24 must not capture the entire subnet implicitly.
		if ip.Is4() {
			req := alias4{}
			copy(req.Name[:], name)
			req.Addr = unix.RawSockaddrInet4{Len: 16, Family: unix.AF_INET, Addr: ip.As4()}
			req.Dst = req.Addr
			req.Mask = unix.RawSockaddrInet4{Len: 16, Family: unix.AF_INET, Addr: [4]byte{255, 255, 255, 255}}
			err = ioctl(fd, unix.SIOCAIFADDR, unsafe.Pointer(&req))
		} else {
			req := alias6{}
			copy(req.Name[:], name)
			req.Addr = unix.RawSockaddrInet6{Len: 28, Family: unix.AF_INET6, Addr: ip.As16()}
			// IPv6 aliases on utun need no point-to-point destination.
			req.Mask = unix.RawSockaddrInet6{Len: 28, Family: unix.AF_INET6}
			for i := range req.Mask.Addr {
				req.Mask.Addr[i] = 255
			}
			req.Flags = 0x0020 // IN6_IFF_NODAD: no neighbor on a point-to-point TUN.
			req.Lifetime = lifetime6{Valid: ^uint32(0), PreferredLifetime: ^uint32(0)}
			const siocaifaddr6 = 0x80000000 | 128<<16 | 'i'<<8 | 26
			err = ioctl(fd, siocaifaddr6, unsafe.Pointer(&req))
		}
		unix.Close(fd)
		if err != nil {
			return 0, err
		}
	}
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return 0, err
	}
	defer unix.Close(fd)
	unix.CloseOnExec(fd)
	req := flagsRequest{}
	copy(req.Name[:], name)
	if err := ioctl(fd, unix.SIOCGIFFLAGS, unsafe.Pointer(&req)); err != nil {
		return 0, err
	}
	req.Flags |= unix.IFF_UP
	if err := ioctl(fd, unix.SIOCSIFFLAGS, unsafe.Pointer(&req)); err != nil {
		return 0, err
	}
	i, err := net.InterfaceByName(name)
	if err != nil {
		return 0, err
	}
	return i.Index, nil
}

var sequence atomic.Uint32

const ownedRouteFlags = unix.RTF_PROTO1 | unix.RTF_PROTO2

func routeCall(ctx context.Context, m *route.RouteMessage) (*route.RouteMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fd, err := unix.Socket(unix.AF_ROUTE, unix.SOCK_RAW, unix.AF_UNSPEC)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	unix.CloseOnExec(fd)
	if err := unix.SetNonblock(fd, true); err != nil {
		return nil, err
	}
	m.Version = unix.RTM_VERSION
	m.ID = uintptr(os.Getpid())
	m.Seq = int(sequence.Add(1))
	data, err := m.Marshal()
	if err != nil {
		return nil, err
	}
	if _, err = unix.Write(fd, data); err != nil {
		return nil, err
	}
	buf := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		if _, err := unix.Poll(poll, 100); err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return nil, err
		}
		if poll[0].Revents&unix.POLLIN == 0 {
			continue
		}
		n, err := unix.Read(fd, buf)
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return nil, err
		}
		messages, err := route.ParseRIB(route.RIBTypeRoute, buf[:n])
		if err != nil {
			return nil, err
		}
		for _, message := range messages {
			if response, ok := message.(*route.RouteMessage); ok && response.ID == m.ID && response.Seq == m.Seq {
				return response, response.Err
			}
		}
	}
}
func routeAddr(ip netip.Addr) route.Addr {
	if ip.Is4() {
		return &route.Inet4Addr{IP: ip.As4()}
	}
	return &route.Inet6Addr{IP: ip.As16()}
}
func addrIP(addr route.Addr) (netip.Addr, bool) {
	switch a := addr.(type) {
	case *route.Inet4Addr:
		return netip.AddrFrom4(a.IP), true
	case *route.Inet6Addr:
		return netip.AddrFrom16(a.IP), true
	}
	return netip.Addr{}, false
}
func lookup(ctx context.Context, ip netip.Addr) (*route.RouteMessage, error) {
	return routeCall(ctx, &route.RouteMessage{Type: unix.RTM_GET, Flags: unix.RTF_UP, Addrs: []route.Addr{unix.RTAX_DST: routeAddr(ip), unix.RTAX_IFP: &route.LinkAddr{}}})
}
func (nativeNetwork) RouteTo(ctx context.Context, ip netip.Addr) (routeSpec, error) {
	m, err := lookup(ctx, ip)
	if err != nil {
		return routeSpec{}, err
	}
	if m.Flags&(unix.RTF_REJECT|unix.RTF_BLACKHOLE) != 0 {
		return routeSpec{}, errors.New("underlay route rejects traffic")
	}
	i, err := net.InterfaceByIndex(m.Index)
	if err != nil {
		return routeSpec{}, err
	}
	// Avoid mistaking an expiring ARP/neighbor clone for a permanent host route.
	prefix := "0.0.0.0/0"
	if ip.Is6() {
		prefix = "::/0"
	}
	if m.Flags&(unix.RTF_HOST|unix.RTF_STATIC) == unix.RTF_HOST|unix.RTF_STATIC {
		prefix = netip.PrefixFrom(ip, ip.BitLen()).String()
	}
	r := routeSpec{Prefix: prefix, Interface: i.Name, Index: i.Index}
	if len(m.Addrs) > unix.RTAX_GATEWAY && m.Flags&unix.RTF_GATEWAY != 0 {
		if ip, ok := addrIP(m.Addrs[unix.RTAX_GATEWAY]); ok {
			r.Gateway = ip.String()
		}
	}
	return r, nil
}

func routeMessage(r routeSpec, kind int) *route.RouteMessage {
	p := netip.MustParsePrefix(r.Prefix)
	mask := net.CIDRMask(p.Bits(), p.Addr().BitLen())
	maskIP, _ := netip.AddrFromSlice(mask)
	flags := unix.RTF_UP | unix.RTF_STATIC | ownedRouteFlags
	if p.Bits() == p.Addr().BitLen() {
		flags |= unix.RTF_HOST
	}
	var gateway route.Addr = &route.LinkAddr{Index: r.Index, Name: r.Interface}
	if r.Gateway != "" {
		gateway = routeAddr(netip.MustParseAddr(r.Gateway))
		if g, ok := gateway.(*route.Inet6Addr); ok && netip.MustParseAddr(r.Gateway).IsLinkLocalUnicast() {
			g.ZoneID = r.Index
		}
		flags |= unix.RTF_GATEWAY
	}
	return &route.RouteMessage{Type: kind, Flags: flags, Index: r.Index, Addrs: []route.Addr{unix.RTAX_DST: routeAddr(p.Addr()), unix.RTAX_GATEWAY: gateway, unix.RTAX_NETMASK: routeAddr(maskIP), unix.RTAX_IFP: &route.LinkAddr{Index: r.Index, Name: r.Interface}}}
}
func (nativeNetwork) AddRoute(ctx context.Context, r routeSpec) error {
	i, err := net.InterfaceByIndex(r.Index)
	if err != nil {
		return err
	}
	if i.Name != r.Interface {
		return errors.New("interface identity changed")
	}
	_, err = routeCall(ctx, routeMessage(r, unix.RTM_ADD))
	if errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("route %s already exists; existing routes are not replaced", r.Prefix)
	}
	return err
}
func (nativeNetwork) DeleteRoute(ctx context.Context, r routeSpec) error {
	i, err := net.InterfaceByIndex(r.Index)
	if err != nil {
		interfaces, e := net.Interfaces()
		if e != nil {
			return e
		}
		for _, item := range interfaces {
			if item.Index == r.Index {
				return err
			}
		}
		return nil
	}
	if i.Name != r.Interface {
		return nil
	}
	p := netip.MustParsePrefix(r.Prefix)
	m, err := lookup(ctx, p.Addr())
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	if err != nil {
		return err
	}
	if m.Index != r.Index || m.Flags&ownedRouteFlags != ownedRouteFlags || m.Flags&unix.RTF_HOST == 0 {
		return nil
	}
	if len(m.Addrs) <= unix.RTAX_GATEWAY {
		return nil
	}
	dest, ok := addrIP(m.Addrs[unix.RTAX_DST])
	if !ok || dest != p.Addr() {
		return nil
	}
	if r.Gateway != "" {
		gateway, ok := addrIP(m.Addrs[unix.RTAX_GATEWAY])
		if !ok || gateway.String() != r.Gateway {
			return nil
		}
	} else {
		if _, ok := m.Addrs[unix.RTAX_GATEWAY].(*route.LinkAddr); !ok {
			return nil
		}
	}
	_, err = routeCall(ctx, routeMessage(r, unix.RTM_DELETE))
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	return err
}
func (nativeNetwork) IsUp(name string) (bool, error) {
	i, err := net.InterfaceByName(name)
	if err != nil {
		return false, err
	}
	return i.Flags&net.FlagUp != 0, nil
}
