package securefs

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsPrivateACLAndExclusiveLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := Directory(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "identity")
	if err := Write(path, []byte("synthetic-private-state")); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err != nil {
		t.Fatal(err)
	}
	sid, err := CurrentSID()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + sid + ")(A;;FR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Fatal("accepted world-readable state")
	}
	lockPath := filepath.Join(dir, "daemon.lock")
	lock, err := Lock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := Lock(lockPath); err == nil {
		other.Close()
		t.Fatal("second owner acquired lock")
	}
	lock.Close()
	lock, err = Lock(lockPath)
	if err != nil {
		t.Fatal("lock not released", err)
	}
	lock.Close()
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err == nil {
		if _, err := Read(link); err == nil {
			t.Fatal("accepted reparse path")
		}
	}
}
