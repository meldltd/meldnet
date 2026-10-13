package vpn

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"testing"

	"golang.zx2c4.com/wireguard/tun"
	"meldnet/internal/config"
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

// A Linux-like fake records batch boundaries without touching host networking.
type batchRelayFake struct {
	relayFake
	batch   int
	batches []int
	err     error
}

func (f *batchRelayFake) BatchSize() int { return f.batch }
func (f *batchRelayFake) Write(b [][]byte, _ int) (int, error) {
	f.batches = append(f.batches, len(b))
	return len(b), f.err
}
func TestRelayBatching(t *testing.T) {
	for _, batch := range []int{1, 4, 128} {
		t.Run(fmt.Sprint(batch), func(t *testing.T) {
			f := &batchRelayFake{relayFake: relayFake{done: make(chan struct{})}, batch: batch}
			r := newRelayTUN(f, &config.Node{Settings: config.Settings{Addresses: []string{"10.77.0.1/32"}, Peers: []config.Peer{{AllowedIPs: []string{"10.77.0.2/32"}}, {AllowedIPs: []string{"10.77.0.3/32"}}}}})
			defer r.Close()
			if r.BatchSize() != batch {
				t.Fatal("lost native batch size")
			}
			var packets [][]byte
			for i := 0; i < 9; i++ {
				b := make([]byte, 36)
				p := b[16:]
				p[0] = 0x45
				p[8] = 64
				binary.BigEndian.PutUint16(p[2:4], 20)
				copy(p[12:20], []byte{10, 77, 0, 2, 10, 77, 0, 1})
				packets = append(packets, b)
			}
			if _, err := r.Write(packets, 16); err != nil {
				t.Fatal(err)
			}
			total := 0
			for _, n := range f.batches {
				if n > batch {
					t.Fatal("exceeded native limit")
				}
				total += n
			}
			if total != 9 || len(f.batches) != (9+batch-1)/batch {
				t.Fatalf("bad host batches: %v", f.batches)
			}
			for _, p := range packets {
				p[35] = 3
			}
			if _, err := r.Write(packets, 16); err != nil {
				t.Fatal(err)
			}
			out := make([][]byte, 128)
			sizes := make([]int, 128)
			for i := range out {
				out[i] = make([]byte, 36)
			}
			// Must return the nine queued packets immediately, without waiting
			// for the rest of this 128-packet receive batch.
			n, err := r.Read(out, sizes, 16)
			if err != nil || n != 9 {
				t.Fatalf("read = %d, %v", n, err)
			}
			for i := 0; i < n; i++ {
				if sizes[i] != 20 || out[i][24] != 63 || out[i][35] != 3 {
					t.Fatal("corrupted batch packet")
				}
			}
			f.err = errors.New("host write failed")
			packets[0][35] = 1
			if _, err := r.Write(packets[:1], 16); !errors.Is(err, f.err) {
				t.Fatalf("lost host error: %v", err)
			}
		})
	}
}
func TestRelayReadBoundsAndClose(t *testing.T) {
	f := &relayFake{done: make(chan struct{})}
	r := newRelayTUN(f, &config.Node{Settings: config.Settings{Addresses: []string{"10.77.0.1/32"}}})
	for _, tc := range []struct {
		bufs   [][]byte
		sizes  []int
		offset int
	}{
		{nil, nil, 0}, {[][]byte{make([]byte, 20)}, nil, 0}, {[][]byte{make([]byte, 20)}, make([]int, 1), -1},
	} {
		if _, err := r.Read(tc.bufs, tc.sizes, tc.offset); !errors.Is(err, io.ErrShortBuffer) {
			t.Fatal(err)
		}
	}
	r.packets <- make([]byte, 20)
	if _, err := r.Read([][]byte{make([]byte, 20)}, make([]int, 1), 16); !errors.Is(err, io.ErrShortBuffer) {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read([][]byte{make([]byte, 20)}, make([]int, 1), 0); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}
