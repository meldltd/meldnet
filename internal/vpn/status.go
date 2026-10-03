package vpn

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"meldnet/internal/config"
)

// The in-process get operation includes secrets. Retain only allowlisted public
// counters. Raw UAPI text never reaches logs, files, or the application API.
type statusWriter struct {
	line    []byte
	peers   map[string]*PeerStatus
	current *PeerStatus
	err     error
}

func newStatusWriter(n *config.Node) *statusWriter {
	s := &statusWriter{peers: map[string]*PeerStatus{}}
	for _, p := range n.Settings.Peers {
		s.peers[p.PublicKey] = &PeerStatus{PublicKey: p.PublicKey}
	}
	return s
}
func (s *statusWriter) Write(data []byte) (int, error) {
	for _, c := range data {
		if c == '\n' {
			s.consume()
			clear(s.line)
			s.line = s.line[:0]
		} else {
			if len(s.line) >= 1024 {
				s.err = errors.New("status line too long")
				return 0, s.err
			}
			s.line = append(s.line, c)
		}
	}
	return len(data), nil
}
func (s *statusWriter) consume() {
	key, value, ok := bytes.Cut(s.line, []byte("="))
	if !ok {
		return
	}
	if string(key) == "public_key" {
		raw, e := hex.DecodeString(string(value))
		if e != nil || len(raw) != 32 {
			s.err = errors.New("invalid public key")
			s.current = nil
			return
		}
		s.current = s.peers[base64.StdEncoding.EncodeToString(raw)]
		return
	}
	if s.current == nil {
		return
	}
	switch string(key) {
	case "last_handshake_time_sec", "rx_bytes", "tx_bytes":
		v, e := strconv.ParseUint(string(value), 10, 64)
		if e != nil {
			s.err = errors.New("invalid counter")
			return
		}
		switch string(key) {
		case "last_handshake_time_sec":
			if v > 0 {
				stamp := time.Unix(int64(v), 0)
				s.current.LastHandshake = &stamp
			}
		case "rx_bytes":
			s.current.Received = v
		case "tx_bytes":
			s.current.Sent = v
		}
	}
}
