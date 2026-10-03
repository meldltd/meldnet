// Package vpn owns tunnel adapters and has no presentation dependencies.
package vpn

import (
	"context"
	"sync"
	"time"

	"meldnet/internal/config"
)

type PeerStatus struct {
	PublicKey     string     `json:"public_key"`
	LastHandshake *time.Time `json:"last_handshake,omitempty"`
	Received      uint64     `json:"received_bytes"`
	Sent          uint64     `json:"sent_bytes"`
}

type State struct {
	Up        bool         `json:"up"`
	Interface string       `json:"interface,omitempty"`
	Peers     []PeerStatus `json:"peers"`
}

// Engine is the only OS-facing dependency of the service.
type Engine interface {
	Kind() string
	Status(context.Context, *config.Node) (State, error)
	Up(context.Context, *config.Node) error
	Down(context.Context, *config.Node) error
}

// Simulator never creates a tunnel, sends packets, or fabricates handshakes.
type Simulator struct {
	mu sync.Mutex
	up bool
}

func (*Simulator) Kind() string { return "simulation" }
func (s *Simulator) Status(_ context.Context, n *config.Node) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := State{Up: s.up, Peers: []PeerStatus{}}
	if s.up {
		state.Interface = "simulation"
	}
	for _, p := range n.Settings.Peers {
		state.Peers = append(state.Peers, PeerStatus{PublicKey: p.PublicKey})
	}
	return state, nil
}
func (s *Simulator) Up(context.Context, *config.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.up = true
	return nil
}
func (s *Simulator) Down(context.Context, *config.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.up = false
	return nil
}
