//go:build darwin || linux

package privatedns

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
	"meldnet/internal/securefs"
)

const marker = "# Managed by Meldnet private DNS"

type fileState struct {
	Version   int
	Path      string
	Original  []byte
	Installed []byte
	Existed   bool
}
type fileChange struct {
	journal string
	state   *fileState
}

func newFileChange(dir string, names ...string) (*fileChange, error) {
	filename := "dns-resolver.json"
	if len(names) > 0 {
		filename = names[0]
	}
	f := &fileChange{journal: filepath.Join(dir, filename)}
	b, e := securefs.Read(f.journal)
	if errors.Is(e, os.ErrNotExist) {
		return f, nil
	}
	if e != nil {
		return nil, e
	}
	var s fileState
	if json.Unmarshal(b, &s) != nil || s.Version != 1 || !filepath.IsAbs(s.Path) || len(s.Original) > 65536 || len(s.Installed) > 65536 || !bytes.Contains(s.Installed, []byte(marker)) {
		return nil, errors.New("invalid DNS recovery journal")
	}
	f.state = &s
	return f, nil
}
func readResolver(path string) ([]byte, error) {
	fd, e := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() || st.Size() > 65536 || st.Mode().Perm()&0022 != 0 {
		return nil, errors.New("resolver file must be a small regular file not writable by group or others")
	}
	return io.ReadAll(io.LimitReader(f, 65537))
}

// Resolver files may be bind-mounted (Docker), so retain their inode. The
// original content is durably journaled first; recovery handles a partial write.
func writeResolver(path string, expected, b []byte, create bool) error {
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW
	if create {
		flags |= unix.O_CREAT | unix.O_EXCL
	}
	fd, e := unix.Open(path, flags, 0644)
	if e != nil {
		return e
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	current, e := io.ReadAll(io.LimitReader(f, 65537))
	if e != nil {
		return e
	}
	if !bytes.Equal(current, expected) {
		return errors.New("resolver changed while applying DNS settings")
	}
	if _, e = f.Seek(0, 0); e != nil {
		return e
	}
	if e = f.Truncate(0); e != nil {
		return e
	}
	if _, e = f.Write(b); e != nil {
		return e
	}
	return f.Sync()
}
func (f *fileChange) install(path string, content []byte, allowExisting bool) error {
	if f.state != nil {
		current, e := readResolver(f.state.Path)
		if e == nil && f.state.Path == path && bytes.Equal(current, f.state.Installed) && bytes.Equal(content, f.state.Installed) {
			return nil
		}
		return errors.New("DNS resolver settings changed; disconnect to restore before retrying")
	}
	original, e := readResolver(path)
	existed := e == nil
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if existed && !allowExisting {
		return errors.New("private DNS resolver file already exists; refusing to replace it")
	}
	if bytes.Contains(original, []byte(marker)) {
		return errors.New("unowned Meldnet DNS settings found; recover using the original state directory")
	}
	s := &fileState{Version: 1, Path: path, Original: original, Installed: content, Existed: existed}
	b, _ := json.Marshal(s)
	if e = securefs.Write(f.journal, b); e != nil {
		return e
	}
	f.state = s
	return writeResolver(path, original, content, !existed)
}
func (f *fileChange) check() error {
	if f.state == nil {
		return errors.New("resolver is not configured")
	}
	b, e := readResolver(f.state.Path)
	if e != nil {
		return e
	}
	if !bytes.Equal(b, f.state.Installed) {
		return errors.New("DNS resolver settings changed externally; disconnect and reconnect to reconfigure")
	}
	return nil
}
func (f *fileChange) restore() error {
	if f.state == nil {
		return nil
	}
	s := f.state
	current, e := readResolver(s.Path)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	// A crash during an in-place write may leave a prefix of our intended content.
	owned := e == nil && (bytes.Equal(current, s.Installed) || (len(current) < len(s.Installed) && bytes.Equal(current, s.Installed[:len(current)])))
	if e == nil && s.Existed && len(current) < len(s.Original) && bytes.Equal(current, s.Original[:len(current)]) {
		owned = true
	}
	if owned {
		if s.Existed {
			if e = writeResolver(s.Path, current, s.Original, false); e != nil {
				return e
			}
		} else if e = os.Remove(s.Path); e != nil {
			return e
		}
	} else if e == nil && bytes.Contains(current, []byte(marker)) {
		return errors.New("DNS resolver contains modified Meldnet settings; recovery journal retained")
	}
	// An external manager replaced/removed our settings. Preserve its content.
	if e = os.Remove(f.journal); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	f.state = nil
	return nil
}
func ensureResolverDirectory(path string) error {
	if e := os.MkdirAll(path, 0755); e != nil {
		return e
	}
	st, e := os.Lstat(path)
	if e != nil {
		return e
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !st.IsDir() || !ok || owner.Uid != uint32(os.Geteuid()) || st.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("unsafe resolver directory %s", path)
	}
	return nil
}
func upstreams(content []byte) ([]string, error) {
	var out []string
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(strings.SplitN(line, "#", 2)[0])
		if len(fields) >= 2 && fields[0] == "nameserver" {
			ip := netParseIP(fields[1])
			if ip == "" {
				return nil, errors.New("unsupported upstream nameserver in resolv.conf")
			}
			if ip == "127.77.0.1" {
				return nil, errors.New("Meldnet DNS proxy cannot forward to itself")
			}
			out = append(out, ip)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("resolv.conf has no upstream nameservers")
	}
	return out, nil
}
