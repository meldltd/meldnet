//go:build darwin || linux

package api

import (
	"context"
	"net"
	"time"
)

const DefaultSocket = "/var/run/meldnet/control.sock"

func dialLocal(ctx context.Context, path string) (net.Conn, error) {
	return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", path)
}
