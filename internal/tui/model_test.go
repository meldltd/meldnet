package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"meldnet/internal/config"
	"meldnet/internal/control"
	"meldnet/internal/service"
	"meldnet/internal/vpn"
)

type localClient struct {
	service *service.Service
	down    int
}

func (c *localClient) Status(ctx context.Context) (service.Status, error) {
	return c.service.Status(ctx), nil
}
func (c *localClient) Configure(ctx context.Context, s config.Settings, r uint64) (config.Public, error) {
	return c.service.Configure(ctx, s, r)
}
func (c *localClient) Connect(ctx context.Context) error { return c.service.Connect(ctx) }
func (c *localClient) Disconnect(ctx context.Context) error {
	c.down++
	return c.service.Disconnect(ctx)
}
func apply(m Model, msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}
func key(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }

func TestSetupPeerConnectAndQuit(t *testing.T) {
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, _ := service.New(store, &vpn.Simulator{})
	c := &localClient{service: s}
	m := New(c)
	m, _ = apply(m, m.fetch()())
	m, _ = apply(m, key(tea.KeyEnter))
	if m.form != "node" || !m.fields[0].Focused() {
		t.Fatal("setup form did not open/focus")
	}
	m.fields[0].SetValue("laptop")
	m.focus = 2
	m, cmd := apply(m, key(tea.KeyEnter))
	if !m.busy || cmd == nil {
		t.Fatal("save not scheduled")
	}
	m, cmd = apply(m, cmd())
	m, _ = apply(m, cmd())
	if !m.status.Initialized || m.form != "" {
		t.Fatal("setup did not complete")
	}
	m, _ = apply(m, key('a'))
	if m.form != "peer" {
		t.Fatal("peer form did not open")
	}
	peer, _ := config.New(config.Settings{Name: "peer", Addresses: []string{"10.77.0.2/24"}})
	values := []string{"server", peer.Public().PublicKey, "vpn.example.com:51820", "10.77.0.2/32", "25"}
	for i, v := range values {
		m.fields[i].SetValue(v)
	}
	m.focus = 4
	m, cmd = apply(m, key(tea.KeyEnter))
	m, cmd = apply(m, cmd())
	m, _ = apply(m, cmd())
	if len(m.status.Node.Settings.Peers) != 1 {
		t.Fatal("peer not saved")
	}
	m, cmd = apply(m, key('c'))
	m, cmd = apply(m, cmd())
	m, _ = apply(m, cmd())
	if !m.status.Tunnel.Up {
		t.Fatal("connect failed")
	}
	if !strings.Contains(m.View().Content, "SIMULATION") {
		t.Fatal("simulation not labeled")
	}
	m, _ = apply(m, key('a'))
	if m.form != "" {
		t.Fatal("allowed edit while running")
	}
	_, cmd = apply(m, key('q'))
	if cmd == nil {
		t.Fatal("quit not scheduled")
	}
	cmd()
	if c.down != 0 || !s.Status(context.Background()).Tunnel.Up {
		t.Fatal("TUI quit stopped VPN")
	}
}

func TestFormValidationAndCancel(t *testing.T) {
	m := New(nil)
	m.online = true
	m.loading = false
	m, _ = apply(m, key(tea.KeyEnter))
	m.focus = 2
	m.fields[2].SetValue("invalid")
	m, cmd := apply(m, key(tea.KeyEnter))
	if cmd != nil || m.message == "" || m.busy {
		t.Fatal("invalid form submitted")
	}
	m, _ = apply(m, key(tea.KeyEscape))
	if m.form != "" {
		t.Fatal("cancel did not close form")
	}
}

func TestResizeAndEmptyView(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {50, 18}, {80, 24}, {120, 40}} {
		m := New(nil)
		m, _ = apply(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		if m.View().Content == "" {
			t.Fatal("empty view")
		}
	}
}

func TestPastingKeyIntoForm(t *testing.T) {
	m := New(nil)
	m.online = true
	m.loading = false
	m, _ = apply(m, key(tea.KeyEnter))
	m, _ = apply(m, tea.PasteMsg{Content: "pasted-node"})
	if m.fields[0].Value() != "pasted-node" {
		t.Fatal("paste was not handled")
	}
}

func TestRemovalUsesConfirmedRevision(t *testing.T) {
	store, _ := config.Open(t.TempDir())
	s, _ := service.New(store, &vpn.Simulator{})
	c := &localClient{service: s}
	settings := config.Settings{Name: "node", Addresses: []string{"10.77.0.1/24"}}
	peer, _ := config.New(settings)
	settings.Peers = []config.Peer{{Name: "peer", PublicKey: peer.Public().PublicKey, AllowedIPs: []string{"10.77.0.2/32"}}}
	p, err := s.Configure(context.Background(), settings, 0)
	if err != nil {
		t.Fatal(err)
	}
	m := New(c)
	m, _ = apply(m, m.fetch()())
	m, _ = apply(m, key('x'))
	settings.Peers[0].Name = "changed-by-other-client"
	if _, err := s.Configure(context.Background(), settings, p.Revision); err != nil {
		t.Fatal(err)
	}
	m, _ = apply(m, m.fetch()())
	_, cmd := apply(m, key('y'))
	result := cmd().(actionMsg)
	if result.err == nil {
		t.Fatal("deleted a peer changed after confirmation began")
	}
	if len(s.Status(context.Background()).Node.Settings.Peers) != 1 {
		t.Fatal("peer removed despite conflict")
	}
}

type managedClient struct {
	*localClient
	joinedName, joinedKey string
}

func (c *managedClient) Network(context.Context) (control.Network, error) {
	return control.Network{Role: "client"}, nil
}
func (c *managedClient) Join(_ context.Context, name, key string) error {
	c.joinedName = name
	c.joinedKey = key
	return nil
}
func TestManagedJoinHidesKeyAndListsDevices(t *testing.T) {
	store, e := config.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	s, _ := service.New(store, &vpn.Simulator{})
	c := &managedClient{localClient: &localClient{service: s}}
	m := New(c)
	m, _ = apply(m, m.fetch()())
	m, _ = apply(m, key('j'))
	if m.form != "join" {
		t.Fatal("join form not opened")
	}
	m.fields[0].SetValue("laptop")
	m.fields[1].SetValue("secret-enrollment-code")
	if strings.Contains(m.View().Content, "secret-enrollment-code") {
		t.Fatal("enrollment secret visible")
	}
	cmd := m.saveForm()
	if cmd == nil {
		t.Fatal("join not scheduled")
	}
	m, _ = apply(m, cmd())
	if c.joinedName != "laptop" || c.joinedKey != "secret-enrollment-code" || m.form != "" {
		t.Fatal("join not submitted")
	}
	m.status.Initialized = true
	m.status.Node = &config.Public{Settings: config.Settings{Name: "laptop", Addresses: []string{"10.77.0.2/32"}}}
	m.network = control.Network{Role: "client", Members: []control.Member{{Name: "primary", IP: "10.77.0.1"}, {Name: "server", IP: "10.77.0.3"}}}
	view := m.View().Content
	if !strings.Contains(view, "server") || !strings.Contains(view, "10.77.0.3") {
		t.Fatal("discovered peer missing")
	}
	m, _ = apply(m, key('a'))
	if m.form != "" {
		t.Fatal("managed configuration editable")
	}
}
