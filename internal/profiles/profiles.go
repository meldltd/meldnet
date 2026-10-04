// Package profiles owns independent network lifecycles behind the local API.
package profiles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"meldnet/internal/config"
	"meldnet/internal/control"
	"meldnet/internal/securefs"
	"meldnet/internal/service"
	"meldnet/internal/vpn"
)

const Default = "default"

var validID = regexp.MustCompile(`^[a-z]([a-z0-9-]{0,29}[a-z0-9])?$`)
var ErrNotFound = errors.New("network profile not found")

type Preference struct {
	ID          string `json:"id"`
	AutoConnect bool   `json:"auto_connect"`
}
type index struct {
	Version  int          `json:"version"`
	Profiles []Preference `json:"profiles"`
}
type Profile struct {
	Preference
	Status  service.Status  `json:"status"`
	Network control.Network `json:"network"`
	Warning string          `json:"warning,omitempty"`
}
type JoinRequest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Key         string `json:"key"`
	AutoConnect bool   `json:"auto_connect"`
	Connect     bool   `json:"connect"`
}
type Runtime struct {
	Service *service.Service
	Manager *control.Manager
	Engine  *vpn.Coordinated
	Close   func() error
}
type Factory func(id, dir string, options *control.Options) (*Runtime, error)
type entry struct {
	Preference
	*Runtime
}
type Manager struct {
	mu      sync.Mutex
	dir     string
	factory Factory
	entries map[string]*entry
	ctx     context.Context
	wg      sync.WaitGroup
	closing bool
}

func New(dir string, factory Factory, options *control.Options) (*Manager, error) {
	m := &Manager{dir: dir, factory: factory, entries: map[string]*entry{}}
	data, err := securefs.Read(filepath.Join(dir, "profiles.json"))
	idx := index{Version: 1, Profiles: []Preference{{ID: Default}}}
	migrate := errors.Is(err, os.ErrNotExist)
	if err != nil && !migrate {
		return nil, err
	}
	if !migrate {
		if json.Unmarshal(data, &idx) != nil || idx.Version != 1 || len(idx.Profiles) == 0 || len(idx.Profiles) > 16 {
			return nil, errors.New("invalid profile index")
		}
	}
	failed := true
	defer func() {
		if failed {
			_ = m.Close()
		}
	}()
	for _, pref := range idx.Profiles {
		if !validID.MatchString(pref.ID) || m.entries[pref.ID] != nil {
			return nil, errors.New("invalid or duplicate profile ID")
		}
		o := options
		if pref.ID != Default {
			o = nil
		}
		runtime, err := factory(pref.ID, m.path(pref.ID), o)
		if err != nil {
			return nil, err
		}
		runtime.Service.SetPaused(true)
		if migrate {
			pref.AutoConnect = runtime.Manager.Snapshot().Role != ""
		}
		m.entries[pref.ID] = &entry{pref, runtime}
	}
	if m.entries[Default] == nil {
		return nil, errors.New("default profile missing")
	}
	if migrate {
		if err = m.save(); err != nil {
			return nil, err
		}
	}
	failed = false
	return m, nil
}
func (m *Manager) path(id string) string {
	if id == Default {
		return m.dir
	}
	return filepath.Join(m.dir, "profiles", id)
}
func (m *Manager) ids() []string {
	ids := make([]string, 0, len(m.entries))
	for id := range m.entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
func (m *Manager) save() error {
	idx := index{Version: 1}
	for _, id := range m.ids() {
		idx.Profiles = append(idx.Profiles, m.entries[id].Preference)
	}
	b, e := json.Marshal(idx)
	if e != nil {
		return e
	}
	return securefs.Write(filepath.Join(m.dir, "profiles.json"), b)
}
func (m *Manager) Runtime(id string) (*Runtime, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[id]
	if e == nil {
		return nil, ErrNotFound
	}
	return e.Runtime, nil
}
func (m *Manager) snapshot(ctx context.Context, e *entry) Profile {
	p := Profile{Preference: e.Preference, Status: e.Service.Status(ctx), Network: e.Manager.Snapshot()}
	var n *config.Node
	if p.Status.Node != nil {
		n = &config.Node{Settings: p.Status.Node.Settings}
	}
	if err := e.Engine.Warning(n); err != nil {
		p.Warning = err.Error()
	}
	for i := range p.Network.Members {
		host := p.Network.Members[i].Hostname
		if strings.HasSuffix(host, ".meldnet.internal") {
			p.Network.Members[i].Hostname = strings.TrimSuffix(host, ".meldnet.internal") + "." + e.ID + ".meldnet.internal"
		}
	}
	return p
}
func (m *Manager) List(ctx context.Context) []Profile {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Profile{}
	for _, id := range m.ids() {
		out = append(out, m.snapshot(ctx, m.entries[id]))
	}
	return out
}
func (m *Manager) SetAuto(id string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[id]
	if e == nil {
		return ErrNotFound
	}
	old := e.AutoConnect
	e.AutoConnect = enabled
	if err := m.save(); err != nil {
		e.AutoConnect = old
		return err
	}
	return nil
}
func connect(ctx context.Context, e *entry) error {
	e.Service.SetPaused(false)
	if e.Manager.Snapshot().Role != "" {
		if err := e.Manager.Sync(ctx); err != nil {
			e.Service.SetPaused(true)
			return err
		}
	}
	if err := e.Service.Connect(ctx); err != nil {
		e.Service.SetPaused(true)
		return err
	}
	return nil
}
func (m *Manager) Connect(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[id]
	if e == nil {
		return ErrNotFound
	}
	return connect(ctx, e)
}
func (m *Manager) Disconnect(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[id]
	if e == nil {
		return ErrNotFound
	}
	return e.Service.Disconnect(ctx)
}
func (m *Manager) Join(ctx context.Context, r JoinRequest) error {
	if !validID.MatchString(r.ID) {
		return errors.New("network name must be 1-31 lowercase letters, digits or hyphens, starting with a letter and ending with a letter or digit")
	}
	if err := control.ValidateJoin(control.JoinRequest{Name: r.Name, Key: r.Key}); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return errors.New("daemon is shutting down")
	}
	e := m.entries[r.ID]
	if e == nil {
		if len(m.entries) >= 16 {
			return errors.New("at most 16 network profiles are supported")
		}
		runtime, err := m.factory(r.ID, m.path(r.ID), nil)
		if err != nil {
			return err
		}
		runtime.Service.SetPaused(true)
		e = &entry{Preference: Preference{ID: r.ID, AutoConnect: r.AutoConnect}, Runtime: runtime}
		m.entries[r.ID] = e
		if err = m.save(); err != nil {
			delete(m.entries, r.ID)
			return errors.Join(err, runtime.Close())
		}
		if m.ctx != nil {
			m.runEntry(e)
		}
	}
	wasEmpty := e.Manager.Snapshot().Role == ""
	// Enroll while paused: registration must not bypass overlap validation or the
	// user's explicit connect selection. Existing profiles retain their policy.
	if e.Service.Status(ctx).Tunnel.Up {
		return errors.New("network is already connected")
	}
	e.Service.SetPaused(true)
	if err := e.Manager.Join(ctx, control.JoinRequest{Name: r.Name, Key: r.Key}); err != nil {
		return err
	}
	if wasEmpty {
		old := e.AutoConnect
		e.AutoConnect = r.AutoConnect
		if err := m.save(); err != nil {
			e.AutoConnect = old
			return err
		}
	}
	if r.Connect {
		return connect(ctx, e)
	}
	return nil
}
func (m *Manager) runEntry(e *entry) {
	m.wg.Add(1)
	go func() { defer m.wg.Done(); e.Manager.Run(m.ctx) }()
}
func (m *Manager) Run(ctx context.Context) {
	m.mu.Lock()
	m.ctx = ctx
	// Ordered startup makes conflicting auto-connect choices deterministic.
	for _, id := range m.ids() {
		e := m.entries[id]
		if e.AutoConnect {
			if err := connect(ctx, e); err != nil && !errors.Is(err, vpn.ErrOverlap) {
				e.Service.SetPaused(false)
			}
		}
		m.runEntry(e)
	}
	m.mu.Unlock()
	<-ctx.Done()
	m.mu.Lock()
	m.closing = true
	m.mu.Unlock()
	m.wg.Wait()
}
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var failures []error
	for _, id := range m.ids() {
		e := m.entries[id]
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		err := e.Service.Disconnect(ctx)
		cancel()
		failures = append(failures, err, e.Close())
	}
	return errors.Join(failures...)
}
func (m *Manager) Serve(ctx context.Context) error {
	r, e := m.Runtime(Default)
	if e != nil {
		return e
	}
	return r.Manager.Serve(ctx)
}
func (p Profile) String() string { return fmt.Sprintf("%s (auto-connect: %t)", p.ID, p.AutoConnect) }

func (m *Manager) Profile(ctx context.Context, id string) (Profile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[id]
	if e == nil {
		return Profile{}, ErrNotFound
	}
	return m.snapshot(ctx, e), nil
}
