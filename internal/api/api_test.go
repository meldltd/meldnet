//go:build darwin || linux

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"meldnet/internal/config"
)

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
