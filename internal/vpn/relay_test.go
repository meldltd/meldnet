package vpn

import (
	"encoding/binary"
	"golang.zx2c4.com/wireguard/tun"
	"io"
	"meldnet/internal/config"
	"testing"
)

func TestRelayTTLAndChecksum(t *testing.T) {
	p := make([]byte, 24)
	p[0] = 0x45
	p[8] = 64
	binary.BigEndian.PutUint16(p[2:4], 24)
	copy(p[12:20], []byte{10, 77, 0, 2, 10, 77, 0, 3})
	out := forwardIPv4(p)
	if len(out) != 24 || out[8] != 63 || p[8] != 64 {
		t.Fatal("bad TTL or mutated input")
	}
	var sum uint32
	for i := 0; i < 20; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(out[i : i+2]))
	}
	for sum>>16 > 0 {
		sum = (sum & 65535) + (sum >> 16)
	}
	if sum != 65535 {
		t.Fatal("invalid checksum")
	}
	p[8] = 1
	if forwardIPv4(p) != nil {
		t.Fatal("forwarded exhausted TTL")
	}
	p[8] = 64
	p[0] = 0x4f
	if forwardIPv4(p) != nil {
		t.Fatal("accepted truncated header")
	}
}

type relayFake struct {
	tun.Device
	done   chan struct{}
	writes int
}

func (f *relayFake) Read(_ [][]byte, _ []int, offset int) (int, error) {
	if offset < 4 {
		panic("utun requires headroom")
	}
	<-f.done
	return 0, io.EOF
}
func (f *relayFake) Write(_ [][]byte, _ int) (int, error) { f.writes++; return 1, nil }
func (f *relayFake) Close() error                         { close(f.done); return nil }
func (f *relayFake) BatchSize() int                       { return 1 }
func TestRelayRestrictsTrafficToMembers(t *testing.T) {
	f := &relayFake{done: make(chan struct{})}
	r := newRelayTUN(f, &config.Node{Settings: config.Settings{Addresses: []string{"10.77.0.1/32"}, Peers: []config.Peer{{AllowedIPs: []string{"10.77.0.2/32"}}, {AllowedIPs: []string{"10.77.0.3/32"}}}}})
	defer r.Close()
	packet := func(src, dst byte) []byte {
		b := make([]byte, 36)
		p := b[16:]
		p[0] = 0x45
		p[8] = 64
		binary.BigEndian.PutUint16(p[2:4], 20)
		copy(p[12:20], []byte{10, 77, 0, src, 10, 77, 0, dst})
		return b
	}
	if _, e := r.Write([][]byte{packet(2, 3)}, 16); e != nil {
		t.Fatal(e)
	}
	buf := make([]byte, 128)
	sizes := make([]int, 1)
	if _, e := r.Read([][]byte{buf}, sizes, 16); e != nil {
		t.Fatal(e)
	}
	if buf[24] != 63 {
		t.Fatal("transit not forwarded")
	}
	_, _ = r.Write([][]byte{packet(2, 1)}, 16)
	if f.writes != 1 {
		t.Fatal("primary traffic did not reach host")
	}
	_, _ = r.Write([][]byte{packet(2, 99), packet(99, 3)}, 16)
	if len(r.packets) != 0 || f.writes != 1 {
		t.Fatal("relayed unknown source or destination")
	}
}
