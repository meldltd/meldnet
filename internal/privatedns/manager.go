package privatedns

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"reflect"
	"sync"
)

type resolver interface {
	Apply(context.Context, string, string, *authority) error
	Restore(context.Context) error
	Mode() string
}
type Manager struct {
	mu        sync.Mutex
	resolver  resolver
	settings  Settings
	zone      authority
	server    *server
	active    bool
	lastError string
}

func New(dir string) (*Manager, error) {
	r, e := newResolver(dir)
	if e != nil {
		return nil, e
	}
	return &Manager{resolver: r}, nil
}
func (m *Manager) Configure(s Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.Server != "" {
		z, e := makeZone(s)
		if e != nil {
			return e
		}
		m.zone.current.Store(z)
	}
	s.Records = sortedRecords(s.Records)
	if m.settings.Server != "" && (m.settings.Server != s.Server || m.settings.Primary != s.Primary || m.settings.Pool != s.Pool) {
		if e := m.down(context.Background()); e != nil {
			return e
		}
	}
	if !reflect.DeepEqual(m.settings, s) {
		m.settings = s
	}
	return nil
}
func (m *Manager) Up(ctx context.Context, iface string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.up(ctx, iface)
	if e != nil {
		m.lastError = e.Error()
	} else {
		m.lastError = ""
	}
	return e
}
func (m *Manager) up(ctx context.Context, iface string) error {
	if m.settings.Server == "" {
		return nil
	}
	if m.settings.Primary && m.server == nil {
		s, e := serve(net.JoinHostPort(m.settings.Server, "53"), &m.zone)
		if e != nil {
			return errors.New("cannot bind private DNS UDP/TCP port 53 on the primary VPN address: " + e.Error())
		}
		m.server = s
	}
	if m.server != nil {
		if e := m.server.health(); e != nil {
			return e
		}
	}
	if e := m.resolver.Apply(ctx, iface, m.settings.Server, &m.zone); e != nil {
		return e
	}
	m.active = true
	return nil
}
func (m *Manager) Down(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.down(ctx)
	if e != nil {
		m.lastError = e.Error()
	} else {
		m.lastError = ""
	}
	return e
}
func (m *Manager) down(ctx context.Context) error {
	// Keep the local fallback DNS proxy alive until host DNS has been restored.
	if e := m.resolver.Restore(ctx); e != nil {
		return e
	}
	m.active = false
	if m.server != nil {
		e := m.server.close()
		m.server = nil
		return e
	}
	return nil
}
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.Server == "" && m.lastError == "" {
		return Status{}
	}
	return Status{Domain: Domain, Server: m.settings.Server, Active: m.active && m.lastError == "", Mode: m.resolver.Mode(), Error: m.lastError}
}
func netParseIP(s string) string {
	ip, e := netip.ParseAddr(s)
	if e != nil {
		return ""
	}
	return ip.String()
}
