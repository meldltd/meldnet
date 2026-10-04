// Package tui is a replaceable local API client. It never owns a VPN engine.
package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"meldnet/internal/config"
	"meldnet/internal/control"
	"meldnet/internal/profiles"
	"meldnet/internal/service"
)

type Client interface {
	Status(context.Context) (service.Status, error)
	Configure(context.Context, config.Settings, uint64) (config.Public, error)
	Connect(context.Context) error
	Disconnect(context.Context) error
}

type networkClient interface {
	Network(context.Context) (control.Network, error)
	Join(context.Context, string, string) error
}
type networkMsg struct {
	profile string
	network control.Network
	err     error
}
type statusMsg struct {
	profile string
	status  service.Status
	err     error
}
type actionMsg struct {
	err   error
	saved bool
}
type tickMsg struct{}

type Model struct {
	profiles        []profiles.Profile
	profilesPage    bool
	profileSelected int
	client          Client
	network         control.Network
	status          service.Status
	online          bool
	loading         bool
	busy            bool
	width, height   int
	selected        int
	message         string
	form            string
	fields          []textinput.Model
	labels          []string
	focus           int
	draft           config.Settings
	revision        uint64
	editing         int
	confirm         bool
}

func New(c Client) Model { return Model{client: c, loading: true, width: 80, height: 24, editing: -1} }
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.fetch(), m.fetchNetwork(), m.fetchProfiles(), tick())
}
func tick() tea.Cmd { return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return tickMsg{} }) }
func (m Model) fetch() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		s, err := m.client.Status(ctx)
		return statusMsg{profile: m.profileID(), status: s, err: err}
	}
}

func (m Model) fetchNetwork() tea.Cmd {
	c, ok := m.client.(networkClient)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		n, e := c.Network(ctx)
		return networkMsg{profile: m.profileID(), network: n, err: e}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case profilesMsg:
		if msg.err == nil {
			m.profiles = msg.profiles
			m.profileSelected = min(m.profileSelected, max(0, len(m.profiles)-1))
		}
	case networkMsg:
		if msg.profile != m.profileID() {
			return m, nil
		}
		if msg.err == nil {
			m.network = msg.network
		} else if m.network.Role != "" {
			m.network.Error = "Cannot refresh device directory."
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		for i := range m.fields {
			m.fields[i].SetWidth(max(12, min(70, m.width-10)))
		}
	case statusMsg:
		if msg.profile != m.profileID() {
			return m, nil
		}
		m.loading = false
		m.online = msg.err == nil
		if msg.err != nil {
			m.message = msg.err.Error()
			return m, nil
		}
		m.status = msg.status
		if m.status.Node != nil {
			m.selected = min(m.selected, max(0, m.peerCount()-1))
		}
	case actionMsg:
		m.busy = false
		if msg.err != nil {
			m.message = msg.err.Error()
		} else {
			m.message = "Done."
			if msg.saved {
				m.form = ""
				m.fields = nil
				m.message = "Configuration saved."
			}
		}
		m.loading = true
		return m, tea.Batch(m.fetch(), m.fetchNetwork(), m.fetchProfiles())
	case tickMsg:
		if !m.busy && !m.loading {
			m.loading = true
			return m, tea.Batch(m.fetch(), m.fetchNetwork(), m.fetchProfiles(), tick())
		}
		return m, tick()
	case tea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		if m.busy {
			return m, nil
		}
		if m.form != "" {
			return m.updateForm(msg)
		}
		if cmd, handled := m.profileKey(key); handled && key != "q" {
			return m, cmd
		}
		if m.confirm {
			m.confirm = false
			if key == "y" {
				cmd := m.removePeer()
				return m, cmd
			}
			m.message = "Removal cancelled."
			return m, nil
		}
		switch key {
		case "q", "esc":
			return m, tea.Quit
		case "r":
			if !m.loading {
				m.loading = true
				m.message = ""
				return m, tea.Batch(m.fetch(), m.fetchNetwork(), m.fetchProfiles())
			}
		}
		if !m.online || m.loading {
			return m, nil
		}
		if !m.status.Initialized || m.network.Pending {
			if key == "j" {
				if _, ok := m.client.(networkClient); ok {
					cmd := m.begin("join", []string{"Device name", "Enrollment key from the primary"}, []string{"", ""})
					m.fields[1].EchoMode = textinput.EchoPassword
					m.fields[1].EchoCharacter = '•'
					return m, cmd
				}
			}
			if !m.status.Initialized && (key == "i" || key == "enter") {
				cmd := m.nodeForm()
				return m, cmd
			}
			return m, nil
		}
		switch key {
		case "up", "k":
			m.selected = max(0, m.selected-1)
		case "down", "j":
			m.selected = min(max(0, m.peerCount()-1), m.selected+1)
		case "c":
			if m.status.Tunnel.Up {
				m.message = "Tunnel is already up."
				return m, nil
			}
			m.busy = true
			m.message = "Starting tunnel…"
			return m, func() tea.Msg { return actionMsg{err: m.client.Connect(context.Background())} }
		case "d":
			m.busy = true
			m.message = "Stopping tunnel…"
			return m, func() tea.Msg { return actionMsg{err: m.client.Disconnect(context.Background())} }
		case "s", "a", "e", "x":
			if m.network.Role != "" {
				m.message = "Network settings are assigned by the primary."
				return m, nil
			}
			if m.status.Tunnel.Up {
				m.message = "Disconnect with d before editing configuration."
				return m, nil
			}
			if key == "s" {
				cmd := m.nodeForm()
				return m, cmd
			}
			if key == "a" {
				cmd := m.peerForm(-1)
				return m, cmd
			}
			if len(m.status.Node.Settings.Peers) == 0 {
				return m, nil
			}
			if key == "e" {
				cmd := m.peerForm(m.selected)
				return m, cmd
			}
			m.confirm = true
			m.snapshot()
			m.editing = m.selected
			m.message = "Remove selected peer? y confirms; any other key cancels."
		}
	}
	if m.form != "" && !m.busy {
		var cmd tea.Cmd
		m.fields[m.focus], cmd = m.fields[m.focus].Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) begin(kind string, labels, values []string) tea.Cmd {
	m.form = kind
	m.labels = labels
	m.fields = make([]textinput.Model, len(labels))
	m.focus = 0
	m.message = ""
	for i := range labels {
		field := textinput.New()
		field.Prompt = "  "
		field.CharLimit = 4096
		field.SetWidth(max(12, min(70, m.width-10)))
		field.SetVirtualCursor(true)
		field.SetValue(values[i])
		m.fields[i] = field
	}
	return m.fields[0].Focus()
}
func (m *Model) snapshot() {
	m.revision = 0
	m.draft = config.Settings{Peers: []config.Peer{}}
	if m.status.Node != nil {
		m.revision = m.status.Node.Revision
		m.draft = m.status.Node.Settings
		m.draft.Peers = append([]config.Peer{}, m.draft.Peers...)
	}
}
func (m *Model) nodeForm() tea.Cmd {
	m.snapshot()
	values := []string{"", "10.77.0.1/24", "51820"}
	if m.status.Initialized {
		values = []string{m.draft.Name, strings.Join(m.draft.Addresses, ", "), strconv.Itoa(m.draft.ListenPort)}
	}
	return m.begin("node", []string{"Node name", "Interface addresses (comma-separated CIDRs)", "UDP listen port (0 = automatic)"}, values)
}
func (m *Model) peerForm(index int) tea.Cmd {
	m.snapshot()
	m.editing = index
	values := []string{"", "", "", "", "25"}
	if index >= 0 {
		p := m.draft.Peers[index]
		values = []string{p.Name, p.PublicKey, p.Endpoint, strings.Join(p.AllowedIPs, ", "), strconv.Itoa(p.Keepalive)}
	}
	return m.begin("peer", []string{"Peer name", "Peer public key", "Endpoint (host:port; blank for an inbound peer)", "Allowed IPs (comma-separated; host /32 or /128)", "Keepalive seconds (0 = disabled)"}, values)
}

func (m Model) updateForm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.form = ""
		m.fields = nil
		m.message = "Edit cancelled."
		return m, nil
	case "tab", "down", "shift+tab", "up":
		m.fields[m.focus].Blur()
		direction := 1
		if msg.String() == "shift+tab" || msg.String() == "up" {
			direction = -1
		}
		m.focus = (m.focus + direction + len(m.fields)) % len(m.fields)
		cmd := m.fields[m.focus].Focus()
		return m, cmd
	case "enter":
		if m.focus < len(m.fields)-1 {
			m.fields[m.focus].Blur()
			m.focus++
			cmd := m.fields[m.focus].Focus()
			return m, cmd
		}
		cmd := m.saveForm()
		return m, cmd
	case "ctrl+s":
		cmd := m.saveForm()
		return m, cmd
	}
	var cmd tea.Cmd
	m.fields[m.focus], cmd = m.fields[m.focus].Update(msg)
	return m, cmd
}

func list(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
func (m *Model) saveForm() tea.Cmd {
	if m.form == "join-profile" {
		c := m.client.(profileClient)
		auto := strings.ToLower(strings.TrimSpace(m.fields[3].Value()))
		connect := strings.ToLower(strings.TrimSpace(m.fields[4].Value()))
		if (auto != "yes" && auto != "no") || (connect != "yes" && connect != "no") {
			m.message = "Use yes or no for startup and connect choices."
			return nil
		}
		req := profiles.JoinRequest{ID: strings.TrimSpace(m.fields[0].Value()), Name: strings.TrimSpace(m.fields[1].Value()), Key: strings.TrimSpace(m.fields[2].Value()), AutoConnect: auto == "yes", Connect: connect == "yes"}
		m.fields[2].SetValue("")
		m.busy = true
		return func() tea.Msg {
			err := c.JoinNetwork(context.Background(), req)
			return actionMsg{err: err, saved: err == nil}
		}
	}

	value := func(i int) string { return strings.TrimSpace(m.fields[i].Value()) }
	if m.form == "join" {
		c, ok := m.client.(networkClient)
		if !ok {
			return nil
		}
		name, key := value(0), value(1)
		m.busy = true
		m.message = "Joining network…"
		return func() tea.Msg { return actionMsg{err: c.Join(context.Background(), name, key), saved: true} }
	}
	if m.form == "node" {
		port, err := strconv.Atoi(value(2))
		if err != nil {
			m.message = "Listen port must be a number."
			return nil
		}
		m.draft.Name = value(0)
		m.draft.Addresses = list(value(1))
		m.draft.ListenPort = port
	} else {
		keepalive, err := strconv.Atoi(value(4))
		if err != nil {
			m.message = "Keepalive must be a number of seconds."
			return nil
		}
		p := config.Peer{Name: value(0), PublicKey: value(1), Endpoint: value(2), AllowedIPs: list(value(3)), Keepalive: keepalive}
		if m.editing >= 0 {
			m.draft.Peers[m.editing] = p
		} else {
			// Do not mutate the draft on a failed submit or subsequent retries.
			draft := m.draft
			draft.Peers = append(append([]config.Peer{}, m.draft.Peers...), p)
			if err := draft.Validate(); err != nil {
				m.message = err.Error()
				return nil
			}
			return m.save(draft)
		}
	}
	if err := m.draft.Validate(); err != nil {
		m.message = err.Error()
		return nil
	}
	return m.save(m.draft)
}
func (m *Model) save(s config.Settings) tea.Cmd {
	m.busy = true
	m.message = "Saving…"
	revision := m.revision
	return func() tea.Msg {
		_, err := m.client.Configure(context.Background(), s, revision)
		return actionMsg{err: err, saved: true}
	}
}
func (m *Model) removePeer() tea.Cmd {
	m.draft.Peers = append(m.draft.Peers[:m.editing], m.draft.Peers[m.editing+1:]...)
	return m.save(m.draft)
}

var (
	accent  = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	muted   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	warning = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
)

func (m Model) View() tea.View {
	width := max(20, min(96, m.width-4))
	var b strings.Builder
	b.WriteString(accent.Render("MELDNET") + "  /  private network\n")
	if c, ok := m.client.(profileClient); ok {
		b.WriteString("Network: " + c.SelectedNetwork() + " · n manage networks\n")
	}
	if m.status.Backend == "simulation" {
		b.WriteString(warning.Render("SIMULATION · no encrypted VPN traffic") + "\n")
	}
	b.WriteString("\n")
	switch {
	case m.width < 64 || m.height < 24:
		b.WriteString("Resize terminal to at least 64 × 24.\nCtrl+C exits; the daemon keeps running.\n")
	case m.form != "":
		title := "Node settings"
		if m.form == "join-profile" {
			title = "Join another network"
		}
		if m.form == "join" {
			title = "Join your network"
		}
		if m.form == "peer" {
			title = "Peer settings"
		}
		b.WriteString(accent.Render(title) + "\n\n")
		for i := range m.fields {
			b.WriteString(m.labels[i] + "\n" + m.fields[i].View() + "\n")
		}
		b.WriteString("\n" + muted.Render("tab/↑↓ fields   enter next/save   ctrl+s save   esc cancel") + "\n")
	case m.profilesPage:
		b.WriteString(m.profilesView())
	case !m.online:
		if m.loading {
			b.WriteString("Contacting daemon…\n")
		} else {
			b.WriteString("Daemon unavailable\n\nStart meldnetd, then press r to retry.\nSee README.md for service setup.\n")
		}
		b.WriteString("\n" + muted.Render("r retry   q quit") + "\n")
	case !m.status.Initialized:
		b.WriteString(accent.Render("Set up this device") + "\n\nPress j and paste an enrollment key from your primary.\nYour VPN address and connections are configured automatically.\n\nThe daemon stores your private key; it stays on this device.\n\n")
		b.WriteString(muted.Render("j join network   enter/i manual setup   q quit") + "\n")
	default:
		n := m.status.Node
		b.WriteString(accent.Render(n.Settings.Name) + "  ·  " + short(strings.Join(n.Settings.Addresses, ", "), max(12, width-len(n.Settings.Name)-5)) + "\n")
		b.WriteString("Public key  " + n.PublicKey + "\n")
		state := "DOWN"
		if m.status.Tunnel.Up {
			state = "UP · " + m.status.Tunnel.Interface
		}
		if m.status.Error != "" {
			state = "UNKNOWN · check diagnostic below"
		}
		b.WriteString("Tunnel      " + accent.Render(state) + "\n\n")
		if m.network.Role != "" {
			m.writeNetwork(&b)
			break
		}
		b.WriteString(fmt.Sprintf("PEERS (%d)\n", len(n.Settings.Peers)))
		if len(n.Settings.Peers) == 0 {
			b.WriteString("No peers yet. Press a to add one.\n")
		}
		visible := max(1, m.height-17)
		start := max(0, m.selected-visible+1)
		end := min(len(n.Settings.Peers), start+visible)
		for i := start; i < end; i++ {
			p := n.Settings.Peers[i]
			mark := "  "
			if i == m.selected {
				mark = "> "
			}
			line := fmt.Sprintf("%s%-18.18s %s", mark, p.Name, handshake(m.status, p.PublicKey))
			if i == m.selected {
				line = accent.Render(line)
			}
			b.WriteString(line + "\n")
		}
		if len(n.Settings.Peers) > 0 {
			p := n.Settings.Peers[m.selected]
			endpoint := p.Endpoint
			if endpoint == "" {
				endpoint = "learned from inbound traffic"
			}
			b.WriteString("\nEndpoint  " + short(endpoint, width-10) + "\nRoutes    " + short(strings.Join(p.AllowedIPs, ", "), width-10) + "\n")
			for _, stat := range m.status.Tunnel.Peers {
				if stat.PublicKey == p.PublicKey {
					b.WriteString(fmt.Sprintf("Traffic   ↓ %s   ↑ %s\n", bytes(stat.Received), bytes(stat.Sent)))
				}
			}
		}
		b.WriteString("\n" + muted.Render("c connect  d disconnect  s settings  a add  e edit  x remove") + "\n" + muted.Render("↑↓ select  r refresh  q quit (VPN stays running)") + "\n")
	}
	if m.status.DNS != nil && m.status.DNS.Error != "" {
		b.WriteString("\n" + warning.Render("DNS: "+m.status.DNS.Error) + "\n")
	}
	if m.status.Error != "" {
		b.WriteString("\n" + warning.Render(m.status.Error) + "\n")
	}
	if m.message != "" {
		b.WriteString("\n" + warning.Render(m.message) + "\n")
	}
	view := tea.NewView(lipgloss.NewStyle().Width(width).Margin(1, 2).Render(b.String()))
	view.AltScreen = true
	view.WindowTitle = "Meldnet"
	return view
}

func handshake(s service.Status, key string) string {
	if s.Backend == "simulation" {
		return "simulated · no handshake"
	}
	for _, p := range s.Tunnel.Peers {
		if p.PublicKey == key && p.LastHandshake != nil {
			return "handshake " + time.Since(*p.LastHandshake).Round(time.Second).String() + " ago"
		}
	}
	return "no handshake"
}
func bytes(n uint64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
}

func short(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:max(0, n-3)] + "..."
}

func (m Model) peerCount() int {
	if m.network.Role != "" {
		return len(m.network.Members)
	}
	if m.status.Node == nil {
		return 0
	}
	return len(m.status.Node.Settings.Peers)
}
func (m Model) writeNetwork(b *strings.Builder) {
	b.WriteString(fmt.Sprintf("DEVICES (%d) · %s\n", len(m.network.Members), m.network.Role))
	visible := max(1, m.height-18)
	start := max(0, m.selected-visible+1)
	end := min(len(m.network.Members), start+visible)
	for i := start; i < end; i++ {
		d := m.network.Members[i]
		seen := "not seen recently"
		if time.Since(d.LastSeen) < 15*time.Second {
			seen = "registered · active"
		}
		mark := "  "
		if i == m.selected {
			mark = "> "
		}
		b.WriteString(fmt.Sprintf("%s%-18.18s %-15s %s\n", mark, d.Name, d.IP, seen))
	}
	if m.network.Pending {
		b.WriteString("Enrollment pending. Press j to retry with a new key.\n")
	}
	if len(m.network.Members) == 0 {
		b.WriteString("Waiting for the primary to configure this device.\n")
	}
	if m.selected < len(m.network.Members) && m.network.Members[m.selected].Hostname != "" {
		b.WriteString("\nDNS  " + m.network.Members[m.selected].Hostname + "\n")
	}
	b.WriteString("\nDevice activity reports control contact, not reachability.\n")
	if m.network.Role == "primary" {
		b.WriteString("Issue a single-use key with: meldnet invite\n")
	}
	if m.network.Error != "" {
		b.WriteString(warning.Render(m.network.Error) + "\n")
	}
	b.WriteString("\nc connect  d disconnect  ↑↓ select  r refresh  q quit\n")
}
