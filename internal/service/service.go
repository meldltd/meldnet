// Package service implements the application independently of its frontends.
package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"meldnet/internal/config"
	"meldnet/internal/vpn"
)

var (
	ErrUninitialized = errors.New("initialize this node first")
	ErrConflict      = errors.New("configuration changed; reload and try again")
	ErrRunning       = errors.New("disconnect the tunnel before editing configuration")
)

type Persistence interface {
	Load() (*config.Node, error)
	Save(*config.Node) error
}
type Status struct {
	Initialized bool           `json:"initialized"`
	Backend     string         `json:"backend"`
	Node        *config.Public `json:"node,omitempty"`
	Tunnel      vpn.State      `json:"tunnel"`
	Error       string         `json:"error,omitempty"`
}

type Service struct {
	mu      sync.Mutex
	store   Persistence
	engine  vpn.Engine
	node    *config.Node
	managed bool
	paused  bool
}

func New(store Persistence, engine vpn.Engine) (*Service, error) {
	n, err := store.Load()
	if err != nil {
		return nil, err
	}
	return &Service{store: store, engine: engine, node: n}, nil
}

func clonePublic(n *config.Node) config.Public {
	p := n.Public()
	p.Settings.Addresses = append([]string{}, p.Settings.Addresses...)
	p.Settings.Peers = append([]config.Peer{}, p.Settings.Peers...)
	for i := range p.Settings.Peers {
		p.Settings.Peers[i].AllowedIPs = append([]string{}, p.Settings.Peers[i].AllowedIPs...)
	}
	return p
}

func (s *Service) Status(ctx context.Context) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := Status{Backend: s.engine.Kind(), Tunnel: vpn.State{Peers: []vpn.PeerStatus{}}}
	if s.node == nil {
		return status
	}
	status.Initialized = true
	p := clonePublic(s.node)
	status.Node = &p
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var err error
	status.Tunnel, err = s.engine.Status(ctx, s.node)
	if err != nil {
		status.Error = err.Error()
	}
	return status
}

func (s *Service) Configure(ctx context.Context, settings config.Settings, revision uint64) (config.Public, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.managed {
		return config.Public{}, errors.New("configuration is managed by the primary")
	}
	if err := settings.Validate(); err != nil {
		return config.Public{}, err
	}
	var next *config.Node
	if s.node == nil {
		if revision != 0 {
			return config.Public{}, ErrConflict
		}
		var err error
		next, err = config.New(settings)
		if err != nil {
			return config.Public{}, err
		}
	} else {
		if revision != s.node.Revision {
			return config.Public{}, ErrConflict
		}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		state, err := s.engine.Status(ctx, s.node)
		if err != nil {
			return config.Public{}, fmt.Errorf("cannot safely edit until tunnel state is known: %w", err)
		}
		if state.Up {
			return config.Public{}, ErrRunning
		}
		copy := *s.node
		copy.Settings = settings
		copy.Revision++
		next = &copy
	}
	if err := next.Validate(); err != nil {
		return config.Public{}, err
	}
	if err := s.store.Save(next); err != nil {
		return config.Public{}, errors.New("could not persist private configuration")
	}
	// Detach caller-owned slices before retaining state.
	next.Settings = clonePublic(next).Settings
	s.node = next
	return clonePublic(next), nil
}

func (s *Service) Connect(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.node == nil {
		return ErrUninitialized
	}
	s.paused = false
	if len(s.node.Settings.Peers) == 0 && !s.managed {
		return errors.New("add at least one peer before connecting")
	}
	// A client going away must not interrupt a half-applied network operation.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return s.engine.Up(ctx, s.node)
}

func (s *Service) Disconnect(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused = true
	if s.node == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return s.engine.Down(ctx, s.node)
}
