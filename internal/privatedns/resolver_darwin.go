package privatedns

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type macResolver struct {
	files     *fileChange
	reverse   *fileChange
	directory string
	stateDir  string
	extra     map[string]*fileChange
}

func newResolver(dir string) (resolver, error) {
	f, e := newFileChange(dir)
	if e != nil {
		return nil, e
	}
	reverse, e := newFileChange(dir, "dns-reverse.json")
	if e != nil {
		return nil, e
	}
	if e = errors.Join(f.restore(), reverse.restore()); e != nil {
		return nil, e
	}
	r := &macResolver{files: f, reverse: reverse, directory: "/etc/resolver", stateDir: dir, extra: map[string]*fileChange{}}
	entries, e := os.ReadDir(dir)
	if e != nil {
		return nil, e
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "dns-multi-") && strings.HasSuffix(entry.Name(), ".json") {
			change, e := newFileChange(dir, entry.Name())
			if e != nil {
				return nil, e
			}
			if e = change.restore(); e != nil {
				return nil, e
			}
		}
	}
	return r, nil
}
func (r *macResolver) Apply(_ context.Context, _ string, server string, z *authority) error {
	if e := ensureResolverDirectory(r.directory); e != nil {
		return e
	}
	content := []byte(fmt.Sprintf("%s\ndomain %s\nnameserver %s\ntimeout 2\n", marker, Domain, server))
	if e := r.files.install(filepath.Join(r.directory, Domain), content, false); e != nil {
		return e
	}
	domains := z.current.Load().reverseDomains()
	if r.stateDir == "" { // Existing isolated resolver fixtures use the legacy one-zone journal.
		reverse := domains[0]
		return r.reverse.install(filepath.Join(r.directory, reverse), []byte(fmt.Sprintf("%s\ndomain %s\nnameserver %s\ntimeout 2\n", marker, reverse, server)), false)
	}
	if e := r.reverse.restore(); e != nil {
		return e
	}
	wanted := map[string]bool{}
	for _, domain := range domains {
		wanted[domain] = true
	}
	for domain, change := range r.extra {
		if !wanted[domain] {
			if e := change.restore(); e != nil {
				return e
			}
			delete(r.extra, domain)
		}
	}
	for _, domain := range domains {
		change := r.extra[domain]
		if change == nil {
			var e error
			change, e = newFileChange(r.stateDir, "dns-multi-"+domain+".json")
			if e != nil {
				return e
			}
			r.extra[domain] = change
		}
		if e := change.install(filepath.Join(r.directory, domain), []byte(fmt.Sprintf("%s\ndomain %s\nnameserver %s\ntimeout 2\n", marker, domain, server)), false); e != nil {
			return e
		}
	}
	return nil
}
func (r *macResolver) Restore(context.Context) error {
	var failures []error
	failures = append(failures, r.files.restore(), r.reverse.restore())
	for _, change := range r.extra {
		failures = append(failures, change.restore())
	}
	return errors.Join(failures...)
}
func (r *macResolver) Mode() string { return "macOS split DNS" }
