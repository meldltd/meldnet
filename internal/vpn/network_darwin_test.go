package vpn

import (
	"context"
	"net/netip"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

func TestDarwinRouteLookupReadOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r, err := (nativeNetwork{}).RouteTo(ctx, netip.MustParseAddr("127.0.0.1"))
	if err != nil {
		t.Fatal("native read-only route lookup:", err)
	}
	if r.Interface != "lo0" || r.Index == 0 {
		t.Fatalf("unexpected loopback route: %+v", r)
	}
}

func TestDarwinNativeABILayouts(t *testing.T) {
	if unsafe.Sizeof(alias4{}) != 64 || unsafe.Sizeof(alias6{}) != 128 || unsafe.Sizeof(flagsRequest{}) != 32 {
		t.Fatal("incorrect Darwin ioctl ABI")
	}
	if unsafe.Offsetof(alias6{}.Lifetime) != 104 {
		t.Fatal("incorrect IPv6 lifetime alignment")
	}
}
func TestDarwinRouteEncoding(t *testing.T) {
	for _, prefix := range []string{"10.77.0.2/32", "0.0.0.0/1", "fd77::2/128", "8000::/1"} {
		m := routeMessage(routeSpec{Prefix: prefix, Interface: "utun7", Index: 12}, unix.RTM_ADD)
		m.Version = unix.RTM_VERSION
		m.ID = 12
		m.Seq = 1
		b, err := m.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		messages, err := route.ParseRIB(route.RIBTypeRoute, b)
		if err != nil {
			t.Fatal(err)
		}
		got := messages[0].(*route.RouteMessage)
		ip, ok := addrIP(got.Addrs[unix.RTAX_DST])
		if !ok || ip != netip.MustParsePrefix(prefix).Addr() || got.Index != 12 || got.Flags&ownedRouteFlags != ownedRouteFlags {
			t.Fatal("route encoding lost ownership or destination")
		}
	}
}
