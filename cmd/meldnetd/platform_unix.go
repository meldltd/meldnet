//go:build darwin || linux

package main

import (
	"context"
	"os"
	"os/signal"
	"runtime"
	"syscall"
)

func entry() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx, nil)
}
func defaultUID() int           { return os.Getuid() }
func hasNetworkPrivilege() bool { return os.Geteuid() == 0 }
func defaultDataDirectory() string {
	if runtime.GOOS == "darwin" {
		return "/Library/Application Support/Meldnet"
	}
	return "/var/lib/meldnet"
}
