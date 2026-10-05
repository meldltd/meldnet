package api

import (
	"context"
	"errors"
	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
	"net"
	"strings"
	"time"
)

const DefaultSocket = `\\.\pipe\Meldnet\control`

func pipePath(path string) bool {
	return strings.HasPrefix(path, `\\.\pipe\Meldnet\`) && len(path) > len(`\\.\pipe\Meldnet\`) && !strings.ContainsAny(strings.TrimPrefix(path, `\\.\pipe\Meldnet\`), `\/:`)
}

func ListenForUser(path string, _ int, sid string) (net.Listener, func(), error) {
	if !pipePath(path) {
		return nil, nil, errors.New("control endpoint must be a local Meldnet named pipe")
	}
	allowed, err := windows.StringToSid(sid)
	if err != nil {
		return nil, nil, errors.New("Windows requires --allow-sid with a valid account SID")
	}
	_, _, accountType, lookupErr := allowed.LookupAccount("")
	if lookupErr != nil || accountType != windows.SidTypeUser {
		return nil, nil, errors.New("allow-sid must identify a single Windows user account")
	}
	// Do not grant generic write: it includes FILE_CREATE_PIPE_INSTANCE and would
	// allow the frontend to impersonate the daemon's next pipe instance.
	descriptor := "D:P(A;;GA;;;SY)(A;;0x12019b;;;" + allowed.String() + ")"
	l, err := winio.ListenPipe(path, &winio.PipeConfig{SecurityDescriptor: descriptor, InputBufferSize: 65536, OutputBufferSize: 65536})
	if err != nil {
		return nil, nil, err
	}
	return l, func() { l.Close() }, nil
}

func dialLocal(ctx context.Context, path string) (net.Conn, error) {
	if !pipePath(path) {
		return nil, errors.New("invalid local Meldnet pipe")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	c, err := winio.DialPipeAccess(ctx, path, 0x12019b)
	if err != nil {
		return nil, err
	}
	// Authenticate the server before an enrollment secret can cross the pipe.
	f, ok := c.(interface{ Fd() uintptr })
	if !ok {
		c.Close()
		return nil, errors.New("cannot authenticate pipe server")
	}
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err == nil {
		owner, _, e := sd.Owner()
		err = e
		if err == nil && (owner == nil || !owner.IsWellKnown(windows.WinLocalSystemSid) && !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid)) {
			err = errors.New("pipe server must be owned by SYSTEM or Administrators")
		}
	}
	if err != nil {
		c.Close()
		return nil, errors.New("cannot authenticate privileged pipe server")
	}
	return c, nil
}
