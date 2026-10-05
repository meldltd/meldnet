package main

import (
	"context"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"os"
	"os/signal"
	"path/filepath"
)

func defaultUID() int           { return 0 }
func hasNetworkPrivilege() bool { return windows.GetCurrentProcessToken().IsElevated() }
func defaultDataDirectory() string {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		return `C:\ProgramData\Meldnet\state`
	}
	return filepath.Join(dir, "Meldnet", "state")
}

func entry() error {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	if isService {
		return svc.Run("Meldnet", daemonService{})
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return run(ctx, nil)
}

type daemonService struct{}

func (daemonService) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	status <- svc.Status{State: svc.StartPending}
	done := make(chan error, 1)
	started := make(chan struct{})
	go func() { done <- run(ctx, func() { close(started) }) }()
	forStarting := svc.Status{State: svc.StartPending, WaitHint: 45000}
	status <- forStarting
	for {
		select {
		case <-started:
			status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
			started = nil
		case err := <-done:
			if err != nil {
				return true, 1
			}
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				status <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending, WaitHint: 45000}
				cancel()
				if <-done != nil {
					return true, 1
				}
				return false, 0
			}
		}
	}
}
