package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"meldnet/internal/config"
	"meldnet/internal/service"
	"meldnet/internal/vpn"
)

func newService(t *testing.T) *service.Service {
	t.Helper()
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := service.New(store, &vpn.Simulator{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestUnixSocketClientAndKeyIsolation(t *testing.T) {
	// macOS Unix socket paths have a short limit; avoid long testing.TempDir names.
	dir, err := os.MkdirTemp("", "mn-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "control.sock")
	listener, cleanup, err := Listen(path, os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	server := &http.Server{Handler: Handler(newService(t))}
	defer server.Close()
	go server.Serve(listener)
	client := NewClient(path)
	defer client.Close()
	ctx := context.Background()
	settings := config.Settings{Name: "node", Addresses: []string{"10.77.0.1/24"}, ListenPort: 51820}
	p, err := client.Configure(ctx, settings, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.PublicKey == "" {
		t.Fatal("no identity")
	}
	status, err := client.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(status)
	if strings.Contains(string(data), "private_key") {
		t.Fatal("secret field exposed")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("socket is not owner-only")
	}
	if _, _, err := Listen(path, os.Getuid()); err == nil {
		t.Fatal("second listener stole active socket")
	}
	if _, err := client.Status(ctx); err != nil {
		t.Fatal("second listener broke original")
	}
}

func TestRejectHostOriginAndUnknownFields(t *testing.T) {
	h := Handler(newService(t))
	tests := []struct {
		name, host, origin, body string
		code                     int
	}{
		{"wrong host", "localhost", "", "{}", 403},
		{"browser origin", "meldnet", "https://example.com", "{}", 403},
		{"unknown field", "meldnet", "", `{"private_key":"secret"}`, 400},
		{"trailing json", "meldnet", "", `{} {}`, 400},
		{"oversized", "meldnet", "", `{"settings":{"name":"` + strings.Repeat("a", 129<<10) + `"}}`, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("PUT", "http://"+tt.host+"/v1/config", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", "application/json")
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tt.code {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestRefuseReplacingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Listen(path, os.Getuid()); err == nil {
		t.Fatal("replaced regular file")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "keep" {
		t.Fatal("damaged file")
	}
}
