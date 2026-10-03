package vpn

import (
	"encoding/binary"
	"io"
	"net/netip"
	"sync"

	"golang.zx2c4.com/wireguard/tun"
	"meldnet/internal/config"
)

// relayTUN forwards only enrolled IPv4 members through the WireGuard device.
// Host forwarding, NAT, and privileged helper processes are unnecessary.
type relayTUN struct {
	tun.Device
	local   netip.Addr
	members map[netip.Addr]bool
	packets chan []byte
	done    chan struct{}
	once    sync.Once
}

func newRelayTUN(t tun.Device, n *config.Node) *relayTUN {
	r := &relayTUN{Device: t, members: map[netip.Addr]bool{}, packets: make(chan []byte, 256), done: make(chan struct{})}
	p, _ := netip.ParsePrefix(n.Settings.Addresses[0])
	r.local = p.Addr()
	for _, peer := range n.Settings.Peers {
		for _, s := range peer.AllowedIPs {
			p, e := netip.ParsePrefix(s)
			if e == nil && p.Bits() == 32 {
				r.members[p.Addr()] = true
			}
		}
	}
	go r.readHost()
	return r
}
func (r *relayTUN) readHost() {
	bufs := make([][]byte, r.Device.BatchSize())
	sizes := make([]int, len(bufs))
	for i := range bufs {
		bufs[i] = make([]byte, 65535+16)
	}
	for {
		n, err := r.Device.Read(bufs, sizes, 16)
		if err != nil {
			r.once.Do(func() { close(r.done) })
			return
		}
		for i := 0; i < n; i++ {
			p := append([]byte(nil), bufs[i][16:16+sizes[i]]...)
			select {
			case r.packets <- p:
			case <-r.done:
				return
			}
		}
	}
}
func (r *relayTUN) BatchSize() int { return 1 }
func (r *relayTUN) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	select {
	case <-r.done:
		return 0, io.EOF
	case p := <-r.packets:
		if len(bufs) == 0 || len(bufs[0])-offset < len(p) {
			return 0, io.ErrShortBuffer
		}
		sizes[0] = copy(bufs[0][offset:], p)
		return 1, nil
	}
}
func (r *relayTUN) Write(bufs [][]byte, offset int) (int, error) {
	for i, b := range bufs {
		p := b[offset:]
		if len(p) < 20 || p[0]>>4 != 4 {
			continue
		}
		src := netip.AddrFrom4([4]byte(p[12:16]))
		dst := netip.AddrFrom4([4]byte(p[16:20]))
		if !r.members[src] {
			continue
		}
		if dst == r.local {
			if _, err := r.Device.Write([][]byte{b}, offset); err != nil {
				return i, err
			}
			continue
		}
		if !r.members[dst] {
			continue
		}
		p = forwardIPv4(p)
		if p == nil {
			continue
		}
		select {
		case r.packets <- p:
		case <-r.done:
			return i, io.EOF
		default: /* bounded packet loss under congestion */
		}
	}
	return len(bufs), nil
}
func forwardIPv4(p []byte) []byte {
	if len(p) < 20 || p[0]>>4 != 4 || p[8] <= 1 {
		return nil
	}
	h := int(p[0]&15) * 4
	n := int(binary.BigEndian.Uint16(p[2:4]))
	if h < 20 || h > len(p) || n < h || n > len(p) {
		return nil
	}
	p = append([]byte(nil), p[:n]...)
	p[8]--
	p[10] = 0
	p[11] = 0
	var sum uint32
	for i := 0; i < h; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(p[i : i+2]))
	}
	for sum>>16 != 0 {
		sum = (sum & 65535) + (sum >> 16)
	}
	binary.BigEndian.PutUint16(p[10:12], ^uint16(sum))
	return p
}
func (r *relayTUN) Close() error { r.once.Do(func() { close(r.done) }); return r.Device.Close() }
func (w *WireGuard) SetRelay(n *config.Node, enabled bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if enabled {
		w.relay = n
	} else {
		w.relay = nil
	}
}
