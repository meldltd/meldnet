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
