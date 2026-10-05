//go:build darwin || linux

package vpn

func networkLockPath() (string, error) { return "/var/run/meldnet-network.lock", nil }
