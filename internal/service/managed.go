package service

import (
	"context"
	"errors"
	"meldnet/internal/config"
	"reflect"
	"time"
)

// SetManaged reserves configuration for the registration controller.
func (s *Service) SetManaged() { s.mu.Lock(); defer s.mu.Unlock(); s.managed = true }

// Reconcile preserves the local private key and serializes network changes with
// local API operations. A membership change briefly recreates the tunnel.
func (s *Service) Reconcile(ctx context.Context, settings config.Settings, relay bool) (config.Public, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := settings.Validate(); err != nil {
		return config.Public{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var next *config.Node
	if s.node == nil {
		var err error
		next, err = config.New(settings)
		if err != nil {
			return config.Public{}, err
		}
	} else {
		n := *s.node
		n.Settings = settings
		next = &n
	}
	if err := next.Validate(); err != nil {
		return config.Public{}, err
	}
	settings.Peers = append([]config.Peer{}, settings.Peers...)
	next.Settings = settings
	changed := s.node == nil || !reflect.DeepEqual(s.node.Settings, settings)
	if changed {
		if s.node != nil {
			if err := s.down(ctx); err != nil {
				return config.Public{}, err
			}
			next.Revision++
		}
		if err := s.store.Save(next); err != nil {
			return config.Public{}, errors.New("cannot save managed configuration")
		}
		next.Settings = clonePublic(next).Settings
		s.node = next
	}
	if r, ok := s.engine.(interface{ SetRelay(*config.Node, bool) }); ok {
		r.SetRelay(s.node, relay)
	}
	if !s.paused {
		if err := s.up(ctx); err != nil {
			return config.Public{}, err
		}
	}
	return clonePublic(s.node), nil
}

// Identity initializes local keys without creating an interface.
func (s *Service) Identity(name string) (config.Public, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.node != nil {
		return clonePublic(s.node), nil
	}
	n, e := config.New(config.Settings{Name: name, Addresses: []string{"10.255.255.254/32"}, Peers: []config.Peer{}})
	if e != nil {
		return config.Public{}, e
	}
	if e = s.store.Save(n); e != nil {
		return config.Public{}, e
	}
	s.node = n
	return clonePublic(n), nil
}

// SetPaused controls reconciliation without starting a tunnel or changing startup policy.
func (s *Service) SetPaused(paused bool) { s.mu.Lock(); defer s.mu.Unlock(); s.paused = paused }
