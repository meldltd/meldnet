package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"meldnet/internal/config"
	"meldnet/internal/privatedns"
	"net/http"
	"net/netip"
)

func ValidateJoin(req JoinRequest) error {
	if _, err := parseKey(req.Key); err != nil {
		return err
	}
	return (config.Settings{Name: req.Name, Addresses: []string{"10.77.0.2/32"}}).Validate()
}
func (m *Manager) Join(ctx context.Context, req JoinRequest) error {
	e, err := parseKey(req.Key)
	if err != nil {
		return err
	}
	if err = (config.Settings{Name: req.Name, Addresses: []string{"10.77.0.2/32"}}).Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.Options != nil {
		return errors.New("primary cannot join another network")
	}
	if m.state.Client == nil {
		if m.svc.Status(ctx).Initialized {
			return errors.New("join requires an uninitialized node")
		}
		next := m.state
		next.Client = &clientState{Enrollment: e, Name: req.Name, Credential: secret()}
		if err = save(m.dir, next); err != nil {
			return err
		}
		m.state = next
		m.svc.SetManaged()
		m.network.Role = "client"
	} else if m.state.Client.Name != req.Name || m.state.Client.Enrollment.URL != e.URL || m.state.Client.Enrollment.Pin != e.Pin {
		return errors.New("already joining or joined a network")
	}
	if !m.state.Client.Registered && m.state.Client.Enrollment.Secret != e.Secret {
		copy := *m.state.Client
		copy.Enrollment = e
		next := m.state
		next.Client = &copy
		if err = save(m.dir, next); err != nil {
			return err
		}
		m.state = next
	}
	err = m.syncClient(ctx)
	if err != nil {
		m.network.Error = err.Error()
	}
	return err
}
func (m *Manager) syncClient(ctx context.Context) error {
	s := m.state.Client
	p, e := m.svc.Identity(s.Name)
	if e != nil {
		return e
	}
	client := pinnedClient(s.Enrollment.Pin)
	defer client.CloseIdleConnections()
	send := func(method, path, token string, body io.Reader) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, s.Enrollment.URL+path, body)
		if err != nil {
			return nil, errors.New("invalid primary address")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		return client.Do(req)
	}
	// An earlier registration may have committed even if its response was lost.
	// Try the durable credential before requiring a still-valid enrollment key.
	res, e := send("GET", "/v1/network", s.Credential, nil)
	if e == nil && res.StatusCode == 401 && !s.Registered {
		res.Body.Close()
		b, _ := json.Marshal(registerRequest{Name: s.Name, PublicKey: p.PublicKey, CredentialHash: hash(s.Credential)})
		res, e = send("POST", "/v1/register", s.Enrollment.Secret, bytes.NewReader(b))
	}
	if e != nil {
		return errors.New("cannot authenticate or reach primary HTTPS service")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		if res.StatusCode == 401 && s.Registered {
			if e := m.svc.Disconnect(ctx); e != nil {
				return e
			}
			return errors.New("device authorization revoked; tunnel stopped")
		}
		return errors.New("primary refused registration or synchronization")
	}
	var r registration
	if e = json.NewDecoder(io.LimitReader(res.Body, 128<<10)).Decode(&r); e != nil {
		return errors.New("invalid primary response")
	}
	pool, e := netip.ParsePrefix(r.Pool)
	ip, iperr := netip.ParseAddr(r.IP)
	if e != nil || iperr != nil || !pool.Addr().Is4() || pool != pool.Masked() || pool.Bits() < 16 || pool.Bits() > 28 || !pool.Addr().IsPrivate() || !ip.Is4() || !pool.Contains(ip) || r.SettingsName != s.Name {
		return errors.New("invalid assigned network")
	}
	settings := config.Settings{Name: s.Name, Addresses: []string{r.IP + "/32"}, Peers: []config.Peer{{Name: r.Primary.Name, PublicKey: r.Primary.PublicKey, Endpoint: r.Endpoint, AllowedIPs: []string{r.Pool}, Keepalive: 25}}}
	if e = settings.Validate(); e != nil {
		return errors.New("invalid network configuration from primary")
	}
	if !s.Registered {
		copy := *s
		copy.Registered = true
		copy.Enrollment.Secret = ""
		next := m.state
		next.Client = &copy
		if e = save(m.dir, next); e != nil {
			return e
		}
		m.state = next
	}
	dnsSettings := privatedns.Settings{}
	if r.DNS != nil {
		if r.DNS.Domain != privatedns.Domain || r.DNS.Server != r.Primary.IP {
			return errors.New("invalid private DNS settings from primary")
		}
		dnsSettings = privatedns.Settings{Server: r.DNS.Server, Pool: r.Pool, Records: dnsRecords(r.Members)}
	}
	if e = m.svc.ConfigureDNS(dnsSettings); e != nil {
		return e
	}
	m.network.DNS = r.DNS
	m.network.Members = r.Members
	_, e = m.svc.Reconcile(ctx, settings, false)
	return e
}
