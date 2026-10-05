package api

import (
	"context"
	"github.com/google/uuid"
	"golang.org/x/sys/windows"
	"meldnet/internal/securefs"
	"net/http"
	"testing"
)

func TestWindowsPipePathsAndUserScope(t *testing.T) {
	for _, path := range []string{`\\remote\pipe\Meldnet\control`, `C:\socket`, `\\.\pipe\other\control`, `\\.\pipe\Meldnet\a\b`} {
		if pipePath(path) {
			t.Fatalf("accepted invalid path %q", path)
		}
	}
	if _, _, err := ListenForUser(DefaultSocket, 0, "S-1-1-0"); err == nil {
		t.Fatal("accepted Everyone instead of an individual account")
	}
}

func TestWindowsNamedPipeAPIPrivacyAndOwnership(t *testing.T) {
	// Native only. An elevated test process is needed to authenticate a
	// privileged pipe owner; no TUN, SCM installation or host DNS is involved.
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("requires elevated Windows test process")
	}
	sid, err := securefs.CurrentSID()
	if err != nil {
		t.Fatal(err)
	}
	path := `\\.\pipe\Meldnet\test-` + uuid.NewString()
	l, closeListener, err := ListenForUser(path, 0, sid)
	if err != nil {
		t.Fatal(err)
	}
	defer closeListener()
	server := &http.Server{Handler: Handler(newService(t))}
	defer server.Close()
	go server.Serve(l)
	c := NewClient(path)
	defer c.Close()
	if _, err := c.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if other, closeOther, err := ListenForUser(path, 0, sid); err == nil {
		other.Close()
		closeOther()
		t.Fatal("second listener stole pipe")
	}
}
