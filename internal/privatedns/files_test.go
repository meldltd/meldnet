package privatedns

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestResolverCrashRecovery(t *testing.T) {
	for _, exists := range []bool{false, true} {
		dir := t.TempDir()
		path := filepath.Join(dir, "resolver")
		original := []byte("nameserver 192.0.2.53\nsearch example.test\n")
		if exists {
			if e := os.WriteFile(path, original, 0644); e != nil {
				t.Fatal(e)
			}
		}
		f, e := newFileChange(dir)
		if e != nil {
			t.Fatal(e)
		}
		installed := []byte(marker + "\nnameserver 127.77.0.1\n")
		if e = f.install(path, installed, exists); e != nil {
			t.Fatal(e)
		}
		// Reopen from durable journal, as a new daemon would after SIGKILL.
		recovered, e := newFileChange(dir)
		if e != nil {
			t.Fatal(e)
		}
		if e = recovered.restore(); e != nil {
			t.Fatal(e)
		}
		b, e := os.ReadFile(path)
		if exists {
			if e != nil || !bytes.Equal(b, original) {
				t.Fatal("original resolver not restored")
			}
		} else if !os.IsNotExist(e) {
			t.Fatal("created resolver was not removed")
		}
	}
}
func TestResolverPreservesExternalChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resolver")
	original := []byte("nameserver 192.0.2.53\n")
	if e := os.WriteFile(path, original, 0644); e != nil {
		t.Fatal(e)
	}
	f, _ := newFileChange(dir)
	owned := []byte(marker + "\nnameserver 127.77.0.1\n")
	if e := f.install(path, owned, true); e != nil {
		t.Fatal(e)
	}
	external := []byte("nameserver 192.0.2.99\n")
	if e := os.WriteFile(path, external, 0644); e != nil {
		t.Fatal(e)
	}
	if f.check() == nil {
		t.Fatal("resolver drift was hidden")
	}
	if e := f.restore(); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(path)
	if !bytes.Equal(b, external) {
		t.Fatal("overwrote administrator settings")
	}
	if e := f.install(path, owned, true); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, append(owned, []byte("# edited\n")...), 0644); e != nil {
		t.Fatal(e)
	}
	if f.restore() == nil {
		t.Fatal("discarded modified owned settings")
	}
	if _, e := os.Stat(f.journal); e != nil {
		t.Fatal("failed cleanup lost its journal")
	}
}
func TestPartialWriteAndConflict(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resolver")
	original := []byte("nameserver 192.0.2.53\n")
	os.WriteFile(path, original, 0644)
	f, _ := newFileChange(dir)
	if f.install(path, []byte(marker+"\n"), false) == nil {
		t.Fatal("replaced existing macOS resolver")
	}
	owned := []byte(marker + "\nnameserver 127.77.0.1\n")
	if e := f.install(path, owned, true); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(path, owned[:10], 0644)
	recovered, _ := newFileChange(dir)
	if e := recovered.restore(); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(path)
	if !bytes.Equal(b, original) {
		t.Fatal("partial write not recovered")
	}
	if _, e := upstreams([]byte("nameserver 127.77.0.1\n")); e == nil {
		t.Fatal("proxy loop accepted")
	}
	link := filepath.Join(dir, "symlink")
	os.Symlink(path, link)
	if _, e := readResolver(link); e == nil {
		t.Fatal("followed unexpected symlink")
	}
}
