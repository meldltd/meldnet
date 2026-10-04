package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"meldnet/internal/config"
	"meldnet/internal/control"
	"meldnet/internal/profiles"
	"meldnet/internal/securefs"
	"meldnet/internal/service"
	"meldnet/internal/vpn"
)

func TestProfileAPIScopeValidationAndOverlap(t *testing.T) {
	dir := t.TempDir()
	gate := vpn.NewCoordinator()
	for _, id := range []string{"default", "work"} {
		path := dir
		if id != "default" {
			path = filepath.Join(dir, "profiles", id)
		}
		store, err := config.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		peer, _ := config.New(config.Settings{Name: "peer", Addresses: []string{"10.77.0.1/32"}})
		node, err := config.New(config.Settings{Name: id, Addresses: []string{"10.77.0.2/32"}, Peers: []config.Peer{{Name: "peer", PublicKey: peer.Public().PublicKey, AllowedIPs: []string{"10.77.0.0/24"}}}})
		if err != nil {
			t.Fatal(err)
		}
		if err = store.Save(node); err != nil {
			t.Fatal(err)
		}
	}
	if err := securefs.Write(filepath.Join(dir, "profiles.json"), []byte(`{"version":1,"profiles":[{"id":"default","auto_connect":false},{"id":"work","auto_connect":false}]}`)); err != nil {
		t.Fatal(err)
	}
	manager, err := profiles.New(dir, func(id, path string, o *control.Options) (*profiles.Runtime, error) {
		store, e := config.Open(path)
		if e != nil {
			return nil, e
		}
		engine := gate.Wrap(id, &vpn.Simulator{})
		svc, e := service.New(store, engine)
		if e != nil {
			return nil, e
		}
		m, e := control.New(path, svc, o)
		return &profiles.Runtime{Service: svc, Manager: m, Engine: engine, Close: func() error { return nil }}, e
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	handler := ProfilesHandler(manager)
	call := func(method, path, body, origin string) int {
		req := httptest.NewRequest(method, "http://meldnet"+path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}
	if code := call("POST", "/v1/networks/work/up", "", "https://evil.example"); code != 403 {
		t.Fatal(code)
	}
	if code := call("POST", "/v1/networks/work/up", "", ""); code != 200 {
		t.Fatal(code)
	}
	if code := call("POST", "/v1/up", "", ""); code != 409 {
		t.Fatalf("legacy endpoint bypassed overlap guard: %d", code)
	}
	for _, body := range []string{`{}`, `{"auto_connect":"yes"}`, `{"auto_connect":true,"extra":1}`} {
		if code := call("PUT", "/v1/networks/work/autoconnect", body, ""); code != 400 {
			t.Fatal(code)
		}
	}
	if code := call("PUT", "/v1/networks/work/autoconnect", `{"auto_connect":true}`, ""); code != 200 {
		t.Fatal(code)
	}
	list := manager.List(context.Background())
	data, _ := json.Marshal(list)
	if bytes.Contains(data, []byte("private_key")) {
		t.Fatal("private key in list")
	}
	p, _ := manager.Profile(context.Background(), "work")
	if !p.AutoConnect || !p.Status.Tunnel.Up {
		t.Fatal("preference unexpectedly changed runtime state")
	}
	if code := call("POST", "/v1/networks/missing/up", "", ""); code != 404 {
		t.Fatal(code)
	}
	if code := call("POST", "/v1/networks/work/down", "", ""); code != 200 {
		t.Fatal(code)
	}
	if code := call("POST", "/v1/up", "", ""); code != 200 {
		t.Fatal(code)
	}
}
