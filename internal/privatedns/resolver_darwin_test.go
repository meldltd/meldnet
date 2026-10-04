package privatedns

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMacSplitResolverLifecycle(t *testing.T) {
	dir := t.TempDir()
	f, _ := newFileChange(dir)
	reverse, _ := newFileChange(dir, "dns-reverse.json")
	r := &macResolver{files: f, reverse: reverse, directory: filepath.Join(dir, "resolver")}
	ctx := context.Background()
	z := testAuthority(t)
	if e := r.Apply(ctx, "utun7", "10.77.0.1", z); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{Domain, "0.77.10.in-addr.arpa"} {
		if _, e := os.Stat(filepath.Join(r.directory, name)); e != nil {
			t.Fatal(e)
		}
	}
	if e := r.Apply(ctx, "utun7", "10.77.0.1", z); e != nil {
		t.Fatal(e)
	}
	if e := r.Restore(ctx); e != nil {
		t.Fatal(e)
	}
	files, _ := os.ReadDir(r.directory)
	if len(files) != 0 {
		t.Fatal("resolver files remain")
	}
}

func TestMacMultipleReverseZonesAndRecovery(t *testing.T) {
	dir := t.TempDir()
	resolverDir := filepath.Join(dir, "resolver")
	raw, err := newResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := raw.(*macResolver)
	r.directory = resolverDir
	z, err := makeZone(Settings{Networks: map[string]Settings{"work": testSettings(), "home": secondSettings()}})
	if err != nil {
		t.Fatal(err)
	}
	a := &authority{}
	a.current.Store(z)
	if err = r.Apply(context.Background(), "", "127.0.0.1", a); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{Domain, "0.77.10.in-addr.arpa", "0.88.10.in-addr.arpa"} {
		if _, err = os.Stat(filepath.Join(resolverDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	// Simulated crash: recovery reads only journaled temporary test paths.
	if _, err = newResolver(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(resolverDir)
	if err != nil || len(entries) != 0 {
		t.Fatal("recovery did not restore all resolver files")
	}
}
