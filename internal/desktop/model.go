// Package desktop contains the public-API-only desktop presentation model.
package desktop

import (
	"context"
	"errors"
	"time"

	"meldnet/internal/api"
	"meldnet/internal/profiles"
)

type Backend interface {
	Networks(context.Context) ([]profiles.Profile, error)
	Connect(context.Context, string) error
	Disconnect(context.Context, string) error
	AutoConnect(context.Context, string, bool) error
	Join(context.Context, profiles.JoinRequest) error
}
type Local struct{ Client *api.Client }

func (l Local) Networks(c context.Context) ([]profiles.Profile, error) { return l.Client.Networks(c) }
func (l Local) Connect(c context.Context, id string) error             { return l.Client.ForNetwork(id).Connect(c) }
func (l Local) Disconnect(c context.Context, id string) error {
	return l.Client.ForNetwork(id).Disconnect(c)
}
func (l Local) AutoConnect(c context.Context, id string, b bool) error {
	return l.Client.ForNetwork(id).SetAutoConnect(c, b)
}
func (l Local) Join(c context.Context, r profiles.JoinRequest) error {
	return l.Client.JoinNetwork(c, r)
}

type Action struct {
	Kind, ID   string
	Enabled    bool
	Enrollment profiles.JoinRequest
}
type Result struct {
	Profiles []profiles.Profile
	Message  string
}

// Run is called by one background worker. Always observe after a mutation, even
// on timeout: a failed response does not mean the daemon rolled back enrollment.
// Never expose error bodies, which may reflect a secret supplied to enrollment.
func Run(ctx context.Context, b Backend, a Action) Result {
	var err error
	switch a.Kind {
	case "":
	case "up":
		err = b.Connect(ctx, a.ID)
	case "down":
		err = b.Disconnect(ctx, a.ID)
	case "auto":
		err = b.AutoConnect(ctx, a.ID, a.Enabled)
	case "join":
		err = b.Join(ctx, a.Enrollment)
	default:
		err = errors.New("unknown action")
	}
	r := Result{}
	if err != nil {
		r.Message = "Request failed. Review the refreshed network state before retrying."
	}
	list, e := b.Networks(ctx)
	if e != nil {
		r.Message = "Service unavailable. Check Meldnet service and account access. A pending action may have completed."
		return r
	}
	r.Profiles = list
	return r
}
func State(p profiles.Profile) string {
	if p.Status.Backend == "simulation" {
		return "Simulation — no VPN traffic"
	}
	if p.Status.Tunnel.Up {
		return "Interface up — peer reachability is separate"
	}
	return "Disconnected"
}

type Peer struct{ Label, Evidence, Hostname string }

func Peers(p profiles.Profile, now time.Time) []Peer {
	rows := []Peer{}
	recent := func(t time.Time, d time.Duration) bool { age := now.Sub(t); return !t.IsZero() && age >= 0 && age < d }
	for _, m := range p.Network.Members {
		evidence := "No recent activity"
		handshake := false
		for _, v := range p.Status.Tunnel.Peers {
			if v.PublicKey == m.PublicKey && v.LastHandshake != nil && recent(*v.LastHandshake, 180*time.Second) {
				handshake = true
			}
		}
		switch {
		case p.Status.Backend == "simulation":
			evidence = "Simulation"
		case p.Status.Node != nil && m.PublicKey == p.Status.Node.PublicKey:
			evidence = "This computer"
		case !p.Status.Tunnel.Up:
			evidence = "VPN disconnected"
		case handshake:
			evidence = "Recent handshake"
		case p.Network.Error != "":
			evidence = "Directory stale"
		case recent(m.LastSeen, 15*time.Second):
			evidence = "Active in control directory"
		}
		label := m.Hostname
		if label == "" {
			label = m.Name
		}
		rows = append(rows, Peer{label, evidence, m.Hostname})
	}
	return rows
}
