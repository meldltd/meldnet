package vpn

import (
	"golang.org/x/sys/windows"
	"meldnet/internal/securefs"
	"path/filepath"
)

func networkLockPath() (string, error) {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "Meldnet", "network-owner")
	if err := securefs.Directory(dir); err != nil {
		return "", err
	}
	return filepath.Join(dir, "network.lock"), nil
}
