package control

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"meldnet/internal/config"
	"meldnet/internal/securefs"
	"meldnet/internal/service"
	"meldnet/internal/vpn"
)

func testService(t *testing.T, dir string) *service.Service {
	t.Helper()
	store, e := config.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	s, e := service.New(store, &vpn.Simulator{})
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func testPrimary(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	svc := testService(t, dir)
	cert, _, e := certificate(dir)
	if e != nil {
		t.Fatal(e)
	}
	var app *fiber.App
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res, err := app.Test(r, fiber.TestConfig{Timeout: 20 * time.Second})
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		defer res.Body.Close()
		for k, v := range res.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(res.StatusCode)
		_, _ = io.Copy(w, res.Body)
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}
	server.StartTLS()
	t.Cleanup(server.Close)
	m, e := New(dir, svc, &Options{Listen: ":8443", URL: server.URL, Endpoint: "127.0.0.1:51820", Pool: "10.77.0.0/24", Name: "primary"})
	if e != nil {
		t.Fatal(e)
	}
	if e = m.syncPrimary(context.Background()); e != nil {
		t.Fatal(e)
	}
	app = m.app()
	return m, dir
}
func testClient(t *testing.T) (*Manager, string) {
	t.Helper()
	d := t.TempDir()
	m, e := New(d, testService(t, d), nil)
	if e != nil {
		t.Fatal(e)
	}
	return m, d
}
func TestEnrollmentRecoveryAndRevocation(t *testing.T) {
	ctx := context.Background()
	primary, dir := testPrimary(t)
	key, e := primary.Invite()
	if e != nil {
		t.Fatal(e)
	}
	client, clientDir := testClient(t)
	if e = client.Join(ctx, JoinRequest{Name: "laptop", Key: key}); e != nil {
		t.Fatal(e)
	}
	s := client.svc.Status(ctx)
	if !s.Tunnel.Up || s.Node.Settings.Addresses[0] != "10.77.0.2/32" || len(s.Node.Settings.Peers) != 1 {
		t.Fatalf("unexpected client: %+v", s)
	}
	// The same one-use key cannot register another device.
	other, _ := testClient(t)
	if other.Join(ctx, JoinRequest{Name: "other", Key: key}) == nil {
		t.Fatal("reused enrollment key accepted")
	}
	data, e := securefs.Read(filepath.Join(dir, "network.json"))
	if e != nil {
		t.Fatal(e)
	}
	inv, _ := parseKey(key)
	if strings.Contains(string(data), inv.Secret) || strings.Contains(string(data), client.state.Client.Credential) {
		t.Fatal("server retained bearer credential")
	}
	oldKey := s.Node.PublicKey
	restarted, e := New(clientDir, testService(t, clientDir), nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = restarted.syncClient(ctx); e != nil {
		t.Fatal(e)
	}
	if restarted.svc.Status(ctx).Node.PublicKey != oldKey {
		t.Fatal("identity changed on restart")
	}
	p2, e := New(dir, testService(t, dir), nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(p2.state.Devices) != 1 || p2.state.Devices[0].IP != "10.77.0.2" {
		t.Fatal("registry not durable")
	}
	if e = primary.Revoke("laptop"); e != nil {
		t.Fatal(e)
	}
	if e = restarted.syncClient(ctx); e == nil {
		t.Fatal("revoked client authorized")
	}
	if restarted.svc.Status(ctx).Tunnel.Up {
		t.Fatal("revoked tunnel still up")
	}
	if _, e = client.svc.Configure(ctx, s.Node.Settings, s.Node.Revision); e == nil {
		t.Fatal("manual edit allowed on managed node")
	}
}
func TestConcurrentEnrollmentAndIdempotence(t *testing.T) {
	p, _ := testPrimary(t)
	const count = 8
	var wg sync.WaitGroup
	ips := make(chan string, count)
	for i := 0; i < count; i++ {
		key, e := p.Invite()
		if e != nil {
			t.Fatal(e)
		}
		wg.Add(1)
		go func(i int, key string) {
			defer wg.Done()
			node, e := config.New(config.Settings{Name: "device", Addresses: []string{"10.1.0.1/32"}})
			if e != nil {
				t.Error(e)
				return
			}
			req := registerRequest{Name: string(rune('a' + i)), PublicKey: node.Public().PublicKey, CredentialHash: hash(secret())}
			en, _ := parseKey(key)
			p.mu.Lock()
			defer p.mu.Unlock()
			r, e := p.register(en.Secret, req)
			if e != nil {
				t.Error(e)
				return
			}
			again, e := p.register(en.Secret, req)
			if e != nil || again.IP != r.IP {
				t.Error("retry changed assignment")
			}
			ips <- r.IP
		}(i, key)
	}
	wg.Wait()
	close(ips)
	seen := map[string]bool{}
	for ip := range ips {
		if seen[ip] {
			t.Fatal("duplicate IP")
		}
		seen[ip] = true
	}
	if len(seen) != count {
		t.Fatal("missing clients")
	}
}
func TestTLSAndAuthentication(t *testing.T) {
	p, _ := testPrimary(t)
	key, _ := p.Invite()
	e, _ := parseKey(key)
	client := pinnedClient(strings.Repeat("0", 64))
	defer client.CloseIdleConnections()
	if r, err := client.Get(e.URL + "/v1/network"); err == nil {
		r.Body.Close()
		t.Fatal("wrong pin accepted")
	}
	c := pinnedClient(e.Pin)
	defer c.CloseIdleConnections()
	r, err := c.Get(e.URL + "/v1/network")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 401 {
		t.Fatal("anonymous directory exposed")
	}
	p.mu.Lock()
	p.state.Invites[0].Expires = time.Now().Add(-time.Minute)
	p.mu.Unlock()
	node, _ := config.New(config.Settings{Name: "x", Addresses: []string{"10.1.0.1/32"}})
	if _, err = p.register(e.Secret, registerRequest{Name: "x", PublicKey: node.Public().PublicKey, CredentialHash: hash(secret())}); err == nil {
		t.Fatal("expired invite accepted")
	}
	b, _ := json.Marshal(p.Snapshot())
	if strings.Contains(string(b), e.Secret) {
		t.Fatal("secret in public status")
	}
}

func TestPendingEnrollmentAcceptsReplacementKey(t *testing.T) {
	p, _ := testPrimary(t)
	key, _ := p.Invite()
	en, _ := parseKey(key)
	en.Secret = secret()
	bad, _ := json.Marshal(en)
	c, _ := testClient(t)
	if c.Join(context.Background(), JoinRequest{Name: "retry", Key: "meldnet1." + base64.RawURLEncoding.EncodeToString(bad)}) == nil {
		t.Fatal("invalid invite accepted")
	}
	if !c.Snapshot().Pending {
		t.Fatal("pending enrollment not reported")
	}
	if e := c.Join(context.Background(), JoinRequest{Name: "retry", Key: key}); e != nil {
		t.Fatal(e)
	}
	if c.Snapshot().Pending {
		t.Fatal("successful enrollment remains pending")
	}
}

func TestLostRegistrationResponseRecoversAfterInviteExpires(t *testing.T) {
	p, _ := testPrimary(t)
	key, _ := p.Invite()
	en, _ := parseKey(key)
	c, dir := testClient(t)
	credential := secret()
	c.state.Client = &clientState{Enrollment: en, Name: "interrupted", Credential: credential}
	c.svc.SetManaged()
	pub, e := c.svc.Identity("interrupted")
	if e != nil {
		t.Fatal(e)
	}
	if e = save(dir, c.state); e != nil {
		t.Fatal(e)
	}
	if _, e = p.register(en.Secret, registerRequest{Name: "interrupted", PublicKey: pub.PublicKey, CredentialHash: hash(credential)}); e != nil {
		t.Fatal(e)
	}
	p.state.Invites[0].Expires = time.Now().Add(-time.Hour)
	restarted, e := New(dir, testService(t, dir), nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = restarted.syncClient(context.Background()); e != nil {
		t.Fatal(e)
	}
	if restarted.Snapshot().Pending || !restarted.svc.Status(context.Background()).Tunnel.Up {
		t.Fatal("lost response did not recover")
	}
}
