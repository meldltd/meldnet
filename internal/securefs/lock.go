//go:build darwin || linux

package securefs

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func Lock(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("another daemon holds this state or socket lock")
	}
	return f, nil
}
