package vpn

import (
	"encoding/binary"
	"testing"

	"meldnet/internal/config"
)

// Measures relay bookkeeping with a burst ready for the next encryption batch.
// It is not an encrypted-throughput or NIC benchmark.
func BenchmarkRelayTransit(b *testing.B) {
	f := &relayFake{done: make(chan struct{})}
	r := newRelayTUN(f, &config.Node{Settings: config.Settings{Addresses: []string{"10.77.0.1/32"}, Peers: []config.Peer{{AllowedIPs: []string{"10.77.0.2/32"}}, {AllowedIPs: []string{"10.77.0.3/32"}}}}})
	defer r.Close()
	const count, size = 128, 1200
	in, out := make([][]byte, count), make([][]byte, count)
	sizes := make([]int, count)
	for i := range in {
		in[i] = make([]byte, 16+size)
		p := in[i][16:]
		p[0] = 0x45
		p[8] = 64
		binary.BigEndian.PutUint16(p[2:4], size)
		copy(p[12:20], []byte{10, 77, 0, 2, 10, 77, 0, 3})
		out[i] = make([]byte, 16+size)
	}
	b.ReportAllocs()
	b.SetBytes(count * size)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Write(in, 16); err != nil {
			b.Fatal(err)
		}
		for read := 0; read < count; {
			n, err := r.Read(out, sizes, 16)
			if err != nil {
				b.Fatal(err)
			}
			read += n
		}
	}
}
