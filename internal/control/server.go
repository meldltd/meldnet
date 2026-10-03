package control

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/limiter"
	"meldnet/internal/config"
)

// Serve starts only the primary's HTTPS listener. Fiber prefork is never enabled.
func (m *Manager) Serve(ctx context.Context) error {
	m.mu.Lock()
	o := m.state.Options
	m.mu.Unlock()
	if o == nil {
		<-ctx.Done()
		return nil
	}
	cert, _, e := certificate(m.dir)
	if e != nil {
		return e
	}
	ln, e := net.Listen("tcp", o.Listen)
	if e != nil {
		return e
	}
	app := m.app()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = app.ShutdownWithContext(c)
			_ = ln.Close()
		case <-done:
		}
	}()
	e = app.Listener(tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}), fiber.ListenConfig{DisableStartupMessage: true})
	if ctx.Err() != nil {
		return nil
	}
	return e
}
func (m *Manager) app() *fiber.App {
	app := fiber.New(fiber.Config{Immutable: true, BodyLimit: 8192, Concurrency: 256, ReadTimeout: 10 * time.Second, WriteTimeout: 40 * time.Second, IdleTimeout: 15 * time.Second, ErrorHandler: func(c fiber.Ctx, e error) error { return c.Status(400).JSON(fiber.Map{"error": "invalid request"}) }})
	app.Use(limiter.New(limiter.Config{Max: 4096, Expiration: time.Minute}))
	app.Use(func(c fiber.Ctx) error { c.Set("Cache-Control", "no-store"); return c.Next() })
	app.Post("/v1/register", limiter.New(limiter.Config{Max: 120, Expiration: time.Minute}), func(c fiber.Ctx) error {
		var req registerRequest
		dec := json.NewDecoder(bytes.NewReader(c.Body()))
		dec.DisallowUnknownFields()
		if dec.Decode(&req) != nil || dec.Decode(new(any)) != io.EOF {
			return c.SendStatus(400)
		}
		token := strings.TrimPrefix(c.Get("Authorization"), "Bearer ")
		if !validSecret(token) {
			return c.SendStatus(401)
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		r, e := m.register(token, req)
		if e != nil {
			return c.Status(400).JSON(fiber.Map{"error": e.Error()})
		}
		return c.JSON(r)
	})
	app.Get("/v1/network", func(c fiber.Ctx) error {
		token := strings.TrimPrefix(c.Get("Authorization"), "Bearer ")
		if !validSecret(token) {
			return c.SendStatus(401)
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		h := hash(token)
		for i, d := range m.state.Devices {
			if !d.Revoked && d.Hash == h {
				m.state.Devices[i].LastSeen = time.Now().UTC()
				m.network.Members = m.members()
				return c.JSON(m.registration(m.state.Devices[i]))
			}
		}
		return c.SendStatus(401)
	})
	return app
}
func (m *Manager) register(token string, req registerRequest) (registration, error) {
	denied := errors.New("enrollment refused: invalid, expired, consumed key or conflicting device")
	if m.state.Options == nil || !validHash(req.CredentialHash) || config.ValidateKey(req.PublicKey) != nil {
		return registration{}, denied
	}
	if e := (config.Settings{Name: req.Name, Addresses: []string{"10.77.0.2/32"}}).Validate(); e != nil {
		return registration{}, errors.New("invalid device name")
	}
	idx := -1
	for i, v := range m.state.Invites {
		if v.Hash == hash(token) && time.Now().Before(v.Expires) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return registration{}, denied
	}
	inv := m.state.Invites[idx]
	if inv.PublicKey != "" {
		for _, d := range m.state.Devices {
			if !d.Revoked && inv.PublicKey == req.PublicKey && inv.CredentialHash == req.CredentialHash && d.PublicKey == req.PublicKey && d.Name == req.Name {
				return m.registration(d), nil
			}
		}
		return registration{}, denied
	}
	if len(m.state.Devices) >= 128 || req.Name == m.state.Options.Name || req.PublicKey == m.primary().PublicKey {
		return registration{}, denied
	}
	for _, d := range m.state.Devices {
		if d.Name == req.Name || d.PublicKey == req.PublicKey || d.Hash == req.CredentialHash {
			return registration{}, denied
		}
	}
	pool, _ := netip.ParsePrefix(m.state.Options.Pool)
	ip := pool.Addr().Next().Next()
	for range m.state.Devices {
		ip = ip.Next()
	}
	if !pool.Contains(ip.Next()) {
		return registration{}, errors.New("address pool exhausted")
	}
	d := device{Member: Member{Name: req.Name, PublicKey: req.PublicKey, IP: ip.String(), LastSeen: time.Now().UTC()}, Hash: req.CredentialHash}
	next := m.state
	next.Devices = append(append([]device{}, next.Devices...), d)
	next.Invites = append([]invite{}, next.Invites...)
	next.Invites[idx].PublicKey = req.PublicKey
	next.Invites[idx].CredentialHash = req.CredentialHash
	if e := save(m.dir, next); e != nil {
		return registration{}, errors.New("cannot persist registration")
	}
	m.state = next
	if e := m.syncPrimary(context.Background()); e != nil {
		return registration{}, errors.New("registered; primary tunnel configuration pending, retry join")
	}
	return m.registration(d), nil
}
