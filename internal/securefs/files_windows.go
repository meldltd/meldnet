package securefs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func CurrentSID() (string, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}

// Windows mode bits are not a privacy boundary. Use a protected owner-only DACL.
func privateDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	sid, err := CurrentSID()
	if err != nil {
		return nil, err
	}
	return windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;OICI;FA;;;" + sid + ")")
}

func noReparse(path string) error {
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		ptr, err := windows.UTF16PtrFromString(p)
		if err != nil {
			return err
		}
		attrs, err := windows.GetFileAttributes(ptr)
		if err != nil {
			return err
		}
		if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return errors.New("private state must not traverse reparse points")
		}
		if filepath.Dir(p) == p {
			return nil
		}
	}
}

func checkPrivate(path string) error {
	if err := noReparse(path); err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	sid, err := CurrentSID()
	if err != nil {
		return err
	}
	if owner == nil || owner.String() != sid {
		return errors.New("private state must be owned by daemon account")
	}
	control, _, err := sd.Control()
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if control&windows.SE_DACL_PROTECTED == 0 || acl == nil || acl.AceCount == 0 {
		return errors.New("private state requires a protected owner-only DACL")
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String() != sid {
			return errors.New("private state grants access to another account")
		}
	}
	return nil
}

func protect(path string) error {
	sd, err := privateDescriptor()
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, owner, nil, dacl, nil)
}

func Directory(path string) error {
	if err := mkdirPrivate(path); err != nil {
		return err
	}
	if err := noReparse(path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("state path must be a directory")
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	sid, err := CurrentSID()
	if err != nil {
		return err
	}
	if owner == nil || owner.String() != sid && !(owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) && windows.GetCurrentProcessToken().IsElevated()) {
		return errors.New("state directory must be owned by daemon account")
	}
	if err := protect(path); err != nil {
		return err
	}
	return checkPrivate(path)
}

func mkdirPrivate(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return noReparse(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := mkdirPrivate(filepath.Dir(path)); err != nil {
		return err
	}
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	sd, err := privateDescriptor()
	if err != nil {
		return err
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	if err := windows.CreateDirectory(ptr, sa); err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return err
	}
	return nil
}

func Write(path string, data []byte) error {
	if err := checkPrivate(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".meldnet-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := protect(f.Name()); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	from, err := windows.UTF16PtrFromString(f.Name())
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// Atomic replacement with write-through; directory fsync is not supported.
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func Read(path string) ([]byte, error) {
	if err := checkPrivate(path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > 1<<20 {
		return nil, fmt.Errorf("state file must be regular and at most 1 MiB")
	}
	return io.ReadAll(io.LimitReader(f, 1<<20))
}
