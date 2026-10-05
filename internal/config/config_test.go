package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func settings() Settings {
	return Settings{Name: "test-node", Addresses: []string{"10.77.0.1/24", "fd77::1/64"}, ListenPort: 51820, Peers: []Peer{}}
}

func TestKeysAndPrivateStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	n, err := New(settings())
	if err != nil {
		t.Fatal(err)
	}
	other, _ := New(settings())
	if n.PrivateKey == other.PrivateKey {
		t.Fatal("key reuse")
	}
	if err := ValidateKey(n.Public().PublicKey); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(n); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.PrivateKey != n.PrivateKey || got.Public().PublicKey != n.Public().PublicKey {
		t.Fatal("identity changed on reload")
	}
	info, _ := os.Stat(filepath.Join(dir, "node.json"))
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("unsafe key permissions")
	}
	data, _ := json.Marshal(n.Public())
	if strings.Contains(string(data), n.PrivateKey) || strings.Contains(string(data), "private_key") {
		t.Fatal("private key exposed")
	}
	if runtime.GOOS == "windows" {
		return
	} // DACL privacy is tested in securefs on Windows.
	if err := os.Chmod(filepath.Join(dir, "node.json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("accepted world-readable private state")
	}
}

func TestRejectInvalidSettings(t *testing.T) {
	n, _ := New(settings())
	key := n.Public().PublicKey
	tests := map[string]func(*Settings){
		"name injection":     func(s *Settings) { s.Name = "node\nPostUp=touch /tmp/pwn" },
		"address injection":  func(s *Settings) { s.Addresses = []string{"10.0.0.1/24\nPostUp=bad"} },
		"invalid port":       func(s *Settings) { s.ListenPort = 65536 },
		"bad key":            func(s *Settings) { s.Peers[0].PublicKey = "bad" },
		"endpoint injection": func(s *Settings) { s.Peers[0].Endpoint = "host\nPostUp=bad:99" },
		"endpoint shell":     func(s *Settings) { s.Peers[0].Endpoint = "$(id):99" },
		"host bits":          func(s *Settings) { s.Peers[0].AllowedIPs = []string{"10.77.0.2/24"} },
		"duplicate key":      func(s *Settings) { s.Peers = append(s.Peers, s.Peers[0]); s.Peers[1].Name = "other" },
		"negative keepalive": func(s *Settings) { s.Peers[0].Keepalive = -1 },
		"zero key":           func(s *Settings) { s.Peers[0].PublicKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			s := settings()
			s.Peers = []Peer{{Name: "peer", PublicKey: key, Endpoint: "[2001:db8::1]:51820", AllowedIPs: []string{"10.77.0.2/32"}, Keepalive: 25}}
			if err := s.Validate(); err != nil {
				t.Fatal(err)
			}
			mutate(&s)
			if err := s.Validate(); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
}

func TestRejectOverlapsAndSelf(t *testing.T) {
	n, _ := New(settings())
	a, _ := New(settings())
	b, _ := New(settings())
	n.Settings.Peers = []Peer{{Name: "a", PublicKey: a.Public().PublicKey, AllowedIPs: []string{"10.0.0.0/24"}}, {Name: "b", PublicKey: b.Public().PublicKey, AllowedIPs: []string{"10.0.0.1/32"}}}
	if err := n.Validate(); err == nil {
		t.Fatal("accepted overlapping peers")
	}
	n.Settings.Peers = n.Settings.Peers[:1]
	n.Settings.Peers[0].PublicKey = n.Public().PublicKey
	if err := n.Validate(); err == nil {
		t.Fatal("accepted self peer")
	}
}

func TestStoreRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(link); err == nil {
		t.Fatal("accepted symlink directory")
	}
}
