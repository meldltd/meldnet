//go:build darwin || linux

package api

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"

	"meldnet/internal/securefs"
)

// Listen creates a socket owned by exactly one local client UID. Its directory
// remains owned by the daemon so clients cannot replace the control endpoint.
func Listen(path string, uid int) (net.Listener, func(), error) {
	if !filepath.IsAbs(path) {
		return nil, nil, errors.New("socket path must be absolute")
	}
	dir := filepath.Dir(path)
	_, statErr := os.Lstat(dir)
	created := errors.Is(statErr, os.ErrNotExist)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0022 != 0 {
		return nil, nil, errors.New("socket directory must be owned by daemon user and not group/world writable")
	}
	// systemd's UMask=0077 must not make a newly created control directory
	// inaccessible to the explicitly authorized non-root client.
	if created {
		if err := os.Chmod(dir, 0755); err != nil {
			return nil, nil, err
		}
		info, err = os.Lstat(dir)
		if err != nil {
			return nil, nil, err
		}
	}
	if uid != os.Geteuid() && info.Mode().Perm()&0001 == 0 {
		return nil, nil, errors.New("socket directory must be traversable by the allowed UID (mode 0755)")
	}
	lock, err := securefs.Lock(path + ".lock")
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (net.Listener, func(), error) { lock.Close(); return nil, nil, err }
	if old, err := os.Lstat(path); err == nil {
		if old.Mode()&os.ModeSocket == 0 {
			return fail(errors.New("refusing to replace a non-socket file"))
		}
		if err := os.Remove(path); err != nil {
			return fail(err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fail(err)
	}
	// Restrict permissions from the instant the socket is created, before chown.
	oldMask := syscall.Umask(0077)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	syscall.Umask(oldMask)
	if err != nil {
		return fail(err)
	}
	cleanup := func() { listener.Close(); lock.Close() }
	if err := os.Chmod(path, 0600); err != nil {
		cleanup()
		return nil, nil, err
	}
	if uid != os.Geteuid() {
		if err := os.Chown(path, uid, -1); err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("assign control socket owner: %w", err)
		}
	}
	return &authorizedListener{UnixListener: listener, uid: uint32(uid)}, cleanup, nil
}

type authorizedListener struct {
	*net.UnixListener
	uid uint32
}

func (l *authorizedListener) Accept() (net.Conn, error) {
	for {
		c, err := l.AcceptUnix()
		if err != nil {
			return nil, err
		}
		raw, err := c.SyscallConn()
		var uid uint32
		var credentialErr error
		if err == nil {
			err = raw.Control(func(fd uintptr) { uid, credentialErr = socketUID(int(fd)) })
		}
		if err == nil && credentialErr == nil && (uid == l.uid || uid == 0) {
			return c, nil
		}
		c.Close()
	}
}

func ListenForUser(path string, uid int, sid string) (net.Listener, func(), error) {
	if sid != "" {
		return nil, nil, errors.New("allow-sid is only supported on Windows")
	}
	return Listen(path, uid)
}
