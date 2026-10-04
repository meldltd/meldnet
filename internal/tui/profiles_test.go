package tui

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"meldnet/internal/api"
	"meldnet/internal/profiles"
	"meldnet/internal/service"
)

func TestNetworkSelectionTargetsActionsAndMasksEnrollment(t *testing.T) {
	dir, err := os.MkdirTemp("", "m-ui-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "api.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan string, 4)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Method + " " + r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})}
	go server.Serve(listener)
	defer server.Close()
	client := api.NewClient(path)
	defer client.Close()
	m := New(client)
	m.profiles = []profiles.Profile{{Preference: profiles.Preference{ID: "home"}}, {Preference: profiles.Preference{ID: "work"}, Warning: "ranges overlap"}}
	m.profilesPage = true
	m.profileSelected = 1
	for _, action := range []struct{ key, route string }{{"c", "POST /v1/networks/work/up"}, {"d", "POST /v1/networks/work/down"}, {"a", "PUT /v1/networks/work/autoconnect"}} {
		cmd, handled := m.profileKey(action.key)
		if !handled || cmd == nil {
			t.Fatal("action missing")
		}
		if msg := cmd().(actionMsg); msg.err != nil {
			t.Fatal(msg.err)
		}
		if got := <-requests; got != action.route {
			t.Fatalf("wrong network: %s", got)
		}
	}
	if !strings.Contains(m.profilesView(), "ranges overlap") {
		t.Fatal("conflict warning hidden")
	}
	m.profileKey("enter")
	if m.profileID() != "work" || m.profilesPage {
		t.Fatal("selection lost")
	}
	m, _ = apply(m, statusMsg{profile: "home", status: service.Status{Backend: "stale"}})
	if m.status.Backend == "stale" {
		t.Fatal("late result crossed profiles")
	}
	m.profilesPage = true
	m.profileKey("+")
	m.fields[2].SetValue("private-enrollment-token")
	if strings.Contains(m.fields[2].View(), "private-enrollment-token") {
		t.Fatal("enrollment is visible")
	}
}
