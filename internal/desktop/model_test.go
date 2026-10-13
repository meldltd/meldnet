package desktop

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"meldnet/internal/control"
	"meldnet/internal/profiles"
	"meldnet/internal/vpn"
)

type fake struct {
	calls              []string
	list               []profiles.Profile
	mutateErr, readErr error
	join               profiles.JoinRequest
	enabled            bool
}

func (f *fake) Networks(context.Context) ([]profiles.Profile, error) {
	f.calls = append(f.calls, "read")
	return f.list, f.readErr
}
func (f *fake) Connect(_ context.Context, id string) error {
	f.calls = append(f.calls, "up:"+id)
	return f.mutateErr
}
func (f *fake) Disconnect(_ context.Context, id string) error {
	f.calls = append(f.calls, "down:"+id)
	return f.mutateErr
}
func (f *fake) AutoConnect(_ context.Context, id string, b bool) error {
	f.calls = append(f.calls, "auto:"+id)
	f.enabled = b
	return f.mutateErr
}
func (f *fake) Join(_ context.Context, r profiles.JoinRequest) error {
	f.calls = append(f.calls, "join")
	f.join = r
	return f.mutateErr
}
func TestMutationScopeAndRefresh(t *testing.T) {
	for _, kind := range []string{"up", "down", "auto"} {
		f := &fake{list: []profiles.Profile{{Preference: profiles.Preference{ID: "work", AutoConnect: true}}}}
		r := Run(context.Background(), f, Action{Kind: kind, ID: "work", Enabled: true})
		if !reflect.DeepEqual(f.calls, []string{kind + ":work", "read"}) || len(r.Profiles) != 1 || !r.Profiles[0].AutoConnect {
			t.Fatalf("%s: %#v %#v", kind, f, r)
		}
		if kind == "auto" && !f.enabled {
			t.Fatal("lost preference")
		}
	}
}
func TestAmbiguousJoinAndSecretPrivacy(t *testing.T) {
	f := &fake{mutateErr: errors.New("fixture-enrollment-secret"), list: []profiles.Profile{{Preference: profiles.Preference{ID: "saved"}}}}
	req := profiles.JoinRequest{ID: "saved", Name: "laptop", Key: "fixture-enrollment-secret", AutoConnect: true, Connect: false}
	r := Run(context.Background(), f, Action{Kind: "join", Enrollment: req})
	if f.join != req || !reflect.DeepEqual(f.calls, []string{"join", "read"}) || len(r.Profiles) != 1 || r.Message == "" || strings.Contains(r.Message, req.Key) {
		t.Fatalf("join recovery failed: %#v", r)
	}
	f.readErr = errors.New(req.Key)
	r = Run(context.Background(), f, Action{})
	if r.Profiles != nil || strings.Contains(r.Message, req.Key) || r.Message == "" {
		t.Fatal("stale data or error reflection")
	}
}
func TestEvidence(t *testing.T) {
	now := time.Now()
	p := profiles.Profile{}
	p.Network.Members = []control.Member{{Name: "peer", Hostname: "peer.work.meldnet.internal", PublicKey: "public", LastSeen: now}}
	if Peers(p, now)[0].Evidence != "VPN disconnected" {
		t.Fatal("directory implies connection")
	}
	p.Status.Tunnel.Up = true
	if Peers(p, now)[0].Evidence != "Active in control directory" {
		t.Fatal("missing control evidence")
	}
	p.Network.Error = "offline"
	if Peers(p, now)[0].Evidence != "Directory stale" {
		t.Fatal("stale directory")
	}
	p.Status.Tunnel.Peers = []vpn.PeerStatus{{PublicKey: "public", LastHandshake: &now}}
	if Peers(p, now)[0].Evidence != "Recent handshake" {
		t.Fatal("missing handshake")
	}
	future := now.Add(time.Hour)
	p.Status.Tunnel.Peers[0].LastHandshake = &future
	if Peers(p, now)[0].Evidence == "Recent handshake" {
		t.Fatal("future handshake accepted")
	}
	p.Status.Backend = "simulation"
	if Peers(p, now)[0].Evidence != "Simulation" || !strings.Contains(State(p), "Simulation") {
		t.Fatal("simulation misrepresented")
	}
	if Peers(p, now)[0].Hostname != "peer.work.meldnet.internal" {
		t.Fatal("copy target")
	}
}
