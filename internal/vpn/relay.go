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

// Preserve the native batch size so Linux can amortize encryption and UDP I/O.
func (r *relayTUN) BatchSize() int { return r.Device.BatchSize() }
func (r *relayTUN) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	if len(bufs) == 0 || len(sizes) < len(bufs) || offset < 0 {
		return 0, io.ErrShortBuffer
	}
	select {
	case <-r.done:
		return 0, io.EOF
	default:
	}
	var p []byte
	select {
	case <-r.done:
		return 0, io.EOF
	case p = <-r.packets:
	}
	for i := range bufs {
		if offset > len(bufs[i]) || len(bufs[i])-offset < len(p) {
			return i, io.ErrShortBuffer
		}
		sizes[i] = copy(bufs[i][offset:], p)
		if i+1 == len(bufs) {
			return i + 1, nil
		}
		// Drain queued packets without waiting to fill a batch: sparse traffic
		// must not pay extra latency for throughput optimization.
		select {
		case p = <-r.packets:
		default:
			return i + 1, nil
		}
	}
	return 0, nil
}
func (r *relayTUN) Write(bufs [][]byte, offset int) (int, error) {
	// Keep host-bound packets together for native Linux GRO. Other platforms
	// may accept only one packet per write, so honor the underlying limit.
	var host [][]byte
	for i, b := range bufs {
		if offset < 0 || offset > len(b) {
			return i, io.ErrShortBuffer
		}
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
			if host == nil {
				host = make([][]byte, 0, min(len(bufs), r.Device.BatchSize()))
			}
			host = append(host, b)
			if len(host) == cap(host) {
				if _, err := r.Device.Write(host, offset); err != nil {
					return i, err
				}
				host = host[:0]
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
	if len(host) > 0 {
		if _, err := r.Device.Write(host, offset); err != nil {
			return 0, err
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
