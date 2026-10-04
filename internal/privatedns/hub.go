package privatedns

import (
	"context"
	"errors"
	"sync"
)

// Hub is the one owner of host resolver changes. Per-profile authorities serve
// primaries on their VPN addresses; a local aggregate serves this host's DNS.
type Hub struct {
	mu    sync.Mutex
	local *Manager
	slots map[string]*Slot
}
type Slot struct {
	hub      *Hub
	id       string
	settings Settings
	active   bool
	primary  *Manager
}
type noResolver struct{}

func (noResolver) Apply(context.Context, string, string, *authority) error { return nil }
func (noResolver) Restore(context.Context) error                           { return nil }
func (noResolver) Mode() string                                            { return "VPN authority" }
func NewHub(dir string) (*Hub, error) {
	local, e := New(dir)
	if e != nil {
		return nil, e
	}
	return &Hub{local: local, slots: map[string]*Slot{}}, nil
}
func (h *Hub) Slot(id string) *Slot {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := &Slot{hub: h, id: id, primary: &Manager{resolver: noResolver{}}}
	h.slots[id] = s
	return s
}
func (s *Slot) Configure(settings Settings) error {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	if settings.Server != "" {
		if _, err := makeZone(settings); err != nil {
			return err
		}
	}
	s.settings = settings
	if err := s.primary.Configure(settings); err != nil {
		return err
	}
	if s.active {
		return s.hub.update(context.Background())
	}
	return nil
}
func (h *Hub) update(ctx context.Context) error {
	networks := map[string]Settings{}
	var pools []string
	for id, slot := range h.slots {
		if slot.settings.Pool != "" {
			pools = append(pools, slot.settings.Pool)
		}
		if slot.active && slot.settings.Server != "" {
			networks[id] = slot.settings
		}
	}
	if len(networks) == 0 {
		return h.local.Down(ctx)
	}
	if err := h.local.Configure(Settings{Server: "127.0.0.1", Primary: true, Networks: networks, KnownPools: pools}); err != nil {
		return err
	}
	return h.local.Up(ctx, "")
}
func (s *Slot) Up(ctx context.Context, iface string) error {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	if s.settings.Server == "" {
		return nil
	}
	if s.settings.Primary {
		if err := s.primary.Up(ctx, iface); err != nil {
			return err
		}
	}
	s.active = true
	return s.hub.update(ctx)
}
func (s *Slot) Down(ctx context.Context) error {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	s.active = false
	return errors.Join(s.hub.update(ctx), s.primary.Down(ctx))
}
func (s *Slot) Status() Status {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	if s.settings.Server == "" {
		return Status{}
	}
	result := s.hub.local.Status()
	result.Server = s.settings.Server
	result.Active = result.Active && s.active
	if err := s.primary.Status().Error; err != "" {
		result.Error = err
		result.Active = false
	}
	return result
}
