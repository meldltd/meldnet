package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"meldnet/internal/api"
	"meldnet/internal/profiles"
)

type profileClient interface {
	Networks(context.Context) ([]profiles.Profile, error)
	ForNetwork(string) *api.Client
	SelectedNetwork() string
	JoinNetwork(context.Context, profiles.JoinRequest) error
}
type profilesMsg struct {
	profiles []profiles.Profile
	err      error
}

func (m Model) fetchProfiles() tea.Cmd {
	c, ok := m.client.(profileClient)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		p, err := c.Networks(ctx)
		return profilesMsg{p, err}
	}
}
func (m *Model) profileKey(key string) (tea.Cmd, bool) {
	c, ok := m.client.(profileClient)
	if !ok {
		return nil, false
	}
	if key == "n" {
		m.profilesPage = !m.profilesPage
		return m.fetchProfiles(), true
	}
	if !m.profilesPage {
		return nil, false
	}
	switch key {
	case "esc":
		m.profilesPage = false
	case "up", "k":
		m.profileSelected = max(0, m.profileSelected-1)
	case "down", "j":
		m.profileSelected = min(max(0, len(m.profiles)-1), m.profileSelected+1)
	case "+":
		cmd := m.begin("join-profile", []string{"Network name (lowercase letters, digits, hyphens)", "Device name", "Enrollment key", "Auto-connect at startup (yes/no)", "Connect now (yes/no)"}, []string{"", "", "", "yes", "yes"})
		m.fields[2].EchoMode = textinput.EchoPassword
		m.fields[2].EchoCharacter = '•'
		m.fields[2].CharLimit = 8192
		return cmd, true
	case "enter", "c", "d", "a":
		if len(m.profiles) == 0 {
			return nil, true
		}
		p := m.profiles[m.profileSelected]
		client := c.ForNetwork(p.ID)
		if key == "enter" {
			m.client = client
			m.profilesPage = false
			m.loading = true
			m.selected = 0
			return tea.Batch(m.fetch(), m.fetchNetwork()), true
		}
		m.busy = true
		return func() tea.Msg {
			var err error
			switch key {
			case "c":
				err = client.Connect(context.Background())
			case "d":
				err = client.Disconnect(context.Background())
			case "a":
				err = client.SetAutoConnect(context.Background(), !p.AutoConnect)
			}
			return actionMsg{err: err}
		}, true
	}
	return nil, true
}
func (m Model) profilesView() string {
	var b strings.Builder
	b.WriteString("NETWORKS · independent connections\n\n")
	visible := max(1, (m.height-10)/3)
	start := max(0, m.profileSelected-visible+1)
	for i := start; i < min(len(m.profiles), start+visible); i++ {
		p := m.profiles[i]
		mark := "  "
		if i == m.profileSelected {
			mark = "> "
		}
		state := "disconnected"
		if p.Status.Tunnel.Up {
			state = "interface up"
		}
		if p.Status.Backend == "simulation" {
			state = "simulation · " + state
		}
		fmt.Fprintf(&b, "%s%s · %s · auto-start %t\n", mark, p.ID, state, p.AutoConnect)
		if p.Warning != "" {
			fmt.Fprintf(&b, "  WARNING: %s\n", p.Warning)
		} else if p.Network.Error != "" {
			fmt.Fprintf(&b, "  %s\n", p.Network.Error)
		} else {
			b.WriteString("\n")
		}
	}
	b.WriteString("\n↑↓ select  c connect  d disconnect  a auto-start\nenter peers  + join another network  n back  q quit\n")
	return b.String()
}

func (m Model) profileID() string {
	if c, ok := m.client.(profileClient); ok {
		return c.SelectedNetwork()
	}
	return ""
}
