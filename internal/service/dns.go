package service

import (
	"context"
	"errors"

	"meldnet/internal/privatedns"
)

// DNSLifecycle is optional: simulation never changes OS DNS or binds DNS ports.
type DNSLifecycle interface {
	Configure(privatedns.Settings) error
	Up(context.Context, string) error
	Down(context.Context) error
	Status() privatedns.Status
}

func (s *Service) SetDNS(d DNSLifecycle) { s.mu.Lock(); defer s.mu.Unlock(); s.dns = d }
func (s *Service) ConfigureDNS(settings privatedns.Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ranges, ok := s.engine.(interface{ SetPool(string) }); ok {
		ranges.SetPool(settings.Pool)
	}
	if s.dns == nil {
		return nil
	}
	return s.dns.Configure(settings)
}
func (s *Service) up(ctx context.Context) error {
	if e := s.engine.Up(ctx, s.node); e != nil {
		return e
	}
	if s.dns != nil {
		state, e := s.engine.Status(ctx, s.node)
		if e != nil {
			return e
		}
		if state.Up {
			// DNS has its own visible status. Resolver conflicts must not prevent peer
			// enrollment or tear down otherwise-working encrypted connections.
			_ = s.dns.Up(ctx, state.Interface)
		}
	}
	return nil
}
func (s *Service) down(ctx context.Context) error {
	var dnsErr error
	if s.dns != nil {
		dnsErr = s.dns.Down(ctx)
	}
	return errors.Join(dnsErr, s.engine.Down(ctx, s.node))
}
