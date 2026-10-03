package privatedns

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

type macResolver struct {
	files     *fileChange
	reverse   *fileChange
	directory string
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
	return &macResolver{files: f, reverse: reverse, directory: "/etc/resolver"}, nil
}
func (r *macResolver) Apply(_ context.Context, _ string, server string, z *authority) error {
	if e := ensureResolverDirectory(r.directory); e != nil {
		return e
	}
	content := []byte(fmt.Sprintf("%s\ndomain %s\nnameserver %s\ntimeout 2\n", marker, Domain, server))
	if e := r.files.install(filepath.Join(r.directory, Domain), content, false); e != nil {
		return e
	}
	reverse := reverseDomain(z.current.Load().pool)
	return r.reverse.install(filepath.Join(r.directory, reverse), []byte(fmt.Sprintf("%s\ndomain %s\nnameserver %s\ntimeout 2\n", marker, reverse, server)), false)
}
func (r *macResolver) Restore(context.Context) error {
	return errors.Join(r.files.restore(), r.reverse.restore())
}
func (r *macResolver) Mode() string { return "macOS split DNS" }
