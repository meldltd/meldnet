package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"meldnet/internal/config"
	"meldnet/internal/privatedns"
	"meldnet/internal/service"
)

type Manager struct {
	mu      sync.Mutex
	dir     string
	svc     *service.Service
	state   state
	pin     string
	network Network
}

func New(dir string, svc *service.Service, o *Options) (*Manager, error) {
	s, e := load(dir)
	if e != nil {
		return nil, e
	}
	if e = s.validate(); e != nil {
		return nil, e
	}
	if ((s.Client != nil && s.Client.Registered) || len(s.Devices) > 0) && !svc.Status(context.Background()).Initialized {
		return nil, errors.New("registered network is missing its local WireGuard identity; restore the complete state directory")
	}
	m := &Manager{dir: dir, svc: svc, state: s}
	if o != nil {
		if s.Client != nil {
			return nil, errors.New("a client cannot become a primary")
		}
		if s.Options != nil {
			if *s.Options != *o {
				return nil, errors.New("primary options differ from persisted state")
			}
		} else {
			if svc.Status(context.Background()).Initialized {
				return nil, errors.New("primary requires a fresh state directory")
			}
			if e = o.validate(); e != nil {
				return nil, e
			}
			s.Options = o
			m.state = s
			if e = save(dir, s); e != nil {
				return nil, e
			}
		}
	}
	if m.state.Options != nil {
		if e = m.state.Options.validate(); e != nil {
			return nil, e
		}
		_, m.pin, e = certificate(dir)
		if e != nil {
			return nil, e
		}
		svc.SetManaged()
		m.network.Role = "primary"
	} else if s.Client != nil {
		svc.SetManaged()
		m.network.Role = "client"
	}
	return m, nil
}
func (m *Manager) Snapshot() Network {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := m.network
	n.Pending = m.state.Client != nil && !m.state.Client.Registered
	n.Members = append([]Member{}, n.Members...)
	return n
}
func (m *Manager) primary() Member {
	o := m.state.Options
	p, _ := netip.ParsePrefix(o.Pool)
	pub := ""
	if n := m.svc.Status(context.Background()).Node; n != nil {
		pub = n.PublicKey
	}
	return Member{Name: o.Name, IP: p.Addr().Next().String(), PublicKey: pub, LastSeen: time.Now().UTC()}
}
func (m *Manager) members() []Member {
	members := []Member{m.primary()}
	for _, d := range m.state.Devices {
		if !d.Revoked {
			members = append(members, d.Member)
		}
	}
	assignHostnames(members)
	return members
}
func (m *Manager) syncPrimary(ctx context.Context) error {
	o := m.state.Options
	_, port, _ := net.SplitHostPort(o.Endpoint)
	n, _ := strconv.Atoi(port)
	p := m.primary()
	settings := config.Settings{Name: o.Name, Addresses: []string{p.IP + "/32"}, ListenPort: n, Peers: []config.Peer{}}
	for _, d := range m.state.Devices {
		if !d.Revoked {
			settings.Peers = append(settings.Peers, config.Peer{Name: d.Name, PublicKey: d.PublicKey, AllowedIPs: []string{d.IP + "/32"}})
		}
	}
	members := m.members()
	if e := m.svc.ConfigureDNS(privatedns.Settings{Primary: true, Server: p.IP, Pool: o.Pool, Records: dnsRecords(members)}); e != nil {
		return e
	}
	m.network.DNS = &dnsConfig{Domain: privatedns.Domain, Server: p.IP}
	_, e := m.svc.Reconcile(ctx, settings, true)
	m.network.Members = m.members()
	return e
}
func (m *Manager) Invite() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.Options == nil {
		return "", errors.New("only the primary can issue enrollment keys")
	}
	next := m.state
	next.Invites = nil
	for _, i := range m.state.Invites {
		if time.Now().Before(i.Expires) {
			next.Invites = append(next.Invites, i)
		}
	}
	if len(next.Invites) >= 128 {
		return "", errors.New("too many unexpired enrollment keys")
	}
	token := secret()
	next.Invites = append(next.Invites, invite{Hash: hash(token), Expires: time.Now().Add(time.Hour)})
	if e := save(m.dir, next); e != nil {
		return "", e
	}
	m.state = next
	b, _ := json.Marshal(enrollment{URL: next.Options.URL, Pin: m.pin, Secret: token})
	return "meldnet1." + base64.RawURLEncoding.EncodeToString(b), nil
}
func (m *Manager) Revoke(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.Options == nil {
		return errors.New("only the primary can revoke devices")
	}
	next := m.state
	next.Devices = append([]device{}, next.Devices...)
	for i, d := range next.Devices {
		if d.Name == name && !d.Revoked {
			next.Devices[i].Revoked = true
			if e := save(m.dir, next); e != nil {
				return e
			}
			m.state = next
			return m.syncPrimary(context.Background())
		}
	}
	return errors.New("active device not found")
}
func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		m.mu.Lock()
		var e error
		if m.state.Options != nil {
			e = m.syncPrimary(ctx)
		} else if m.state.Client != nil {
			e = m.syncClient(ctx)
		}
		if e != nil {
			m.network.Error = e.Error()
		} else {
			m.network.Error = ""
		}
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (m *Manager) registration(d device) registration {
	return registration{DNS: &dnsConfig{Domain: privatedns.Domain, Server: m.primary().IP}, SettingsName: d.Name, IP: d.IP, Pool: m.state.Options.Pool, Primary: m.primary(), Endpoint: m.state.Options.Endpoint, Members: m.members()}
}

func dnsRecords(members []Member) []privatedns.Record {
	out := make([]privatedns.Record, 0, len(members))
	for _, m := range members {
		out = append(out, privatedns.Record{Name: m.Name, IP: m.IP, PublicKey: m.PublicKey})
	}
	return out
}
func assignHostnames(members []Member) {
	names := privatedns.Hostnames(dnsRecords(members))
	for i := range members {
		members[i].Hostname = names[members[i].PublicKey]
	}
}

// Sync refreshes one profile without owning its startup policy.
func (m *Manager) Sync(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var err error
	if m.state.Options != nil {
		err = m.syncPrimary(ctx)
	} else if m.state.Client != nil {
		err = m.syncClient(ctx)
	}
	if err != nil {
		m.network.Error = err.Error()
	} else {
		m.network.Error = ""
	}
	return err
}
