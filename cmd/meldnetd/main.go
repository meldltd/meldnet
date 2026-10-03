package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"meldnet/internal/api"
	"meldnet/internal/config"
	"meldnet/internal/control"
	"meldnet/internal/privatedns"
	"meldnet/internal/securefs"
	"meldnet/internal/service"
	"meldnet/internal/vpn"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "meldnetd:", err)
		os.Exit(1)
	}
}
func run() error {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return errors.New("supported platforms are macOS and Linux")
	}
	defaultData := "/var/lib/meldnet"
	if runtime.GOOS == "darwin" {
		defaultData = "/Library/Application Support/Meldnet"
	}
	data := flag.String("data-dir", defaultData, "private daemon state directory")
	socket := flag.String("socket", api.DefaultSocket, "absolute control socket path")
	uid := flag.Int("allow-uid", os.Getuid(), "local UID authorized to control the VPN")
	simulate := flag.Bool("simulate", false, "simulate VPN lifecycle; no tunnel or VPN traffic (registration still uses HTTPS)")
	primary := flag.Bool("primary", false, "initialize a primary registration daemon")
	listen := flag.String("listen", ":8443", "primary HTTPS listen address")
	publicURL := flag.String("public-url", "", "primary reachable HTTPS origin")
	endpoint := flag.String("endpoint", "", "primary reachable WireGuard host:port")
	pool := flag.String("pool", "10.77.0.0/24", "private IPv4 allocation pool")
	name := flag.String("name", "primary", "primary device name")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	if *uid < 0 {
		return errors.New("allow-uid must be nonnegative")
	}
	if !*simulate && os.Geteuid() != 0 {
		return errors.New("the WireGuard daemon requires root; run the TUI as your normal user")
	}
	if *simulate && (*data == defaultData || *socket == api.DefaultSocket) {
		return errors.New("simulation requires explicit --data-dir and --socket paths separate from production")
	}
	absolute, err := filepath.Abs(*data)
	if err != nil {
		return err
	}
	store, err := config.Open(absolute)
	if err != nil {
		return err
	}
	lock, err := securefs.Lock(filepath.Join(absolute, "daemon.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	var engine vpn.Engine = &vpn.Simulator{}
	if !*simulate {
		embedded, openErr := vpn.NewWireGuard(filepath.Join(absolute, "runtime"))
		err = openErr
		if err != nil {
			return err
		}
		engine = embedded
		defer embedded.Close()
	}
	svc, err := service.New(store, engine)
	if err != nil {
		return err
	}
	if !*simulate {
		dns, e := privatedns.New(absolute)
		if e != nil {
			return fmt.Errorf("recover private DNS: %w", e)
		}
		svc.SetDNS(dns)
	}
	var options *control.Options
	if *primary {
		options = &control.Options{Listen: *listen, URL: *publicURL, Endpoint: *endpoint, Pool: *pool, Name: *name}
	}
	manager, err := control.New(absolute, svc, options)
	if err != nil {
		return err
	}
	listener, cleanup, err := api.Listen(*socket, *uid)
	if err != nil {
		return err
	}
	defer cleanup()
	server := &http.Server{Handler: api.Handler(svc, manager), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 40 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 8 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result := make(chan error, 2)
	managedCtx, cancelManaged := context.WithCancel(ctx)
	managedDone := make(chan struct{})
	go func() { defer close(managedDone); manager.Run(managedCtx) }()
	go func() { result <- manager.Serve(managedCtx) }()
	go func() { result <- server.Serve(listener) }()
	fmt.Fprintf(os.Stderr, "meldnetd: backend=%s socket=%s allowed_uid=%d\n", engine.Kind(), *socket, *uid)
	select {
	case <-ctx.Done():
	case err = <-result:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}
	cancelManaged()
	<-managedDone
	shutdown, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	serverErr := server.Shutdown(shutdown)
	tunnelErr := svc.Disconnect(context.Background())
	if tunnelErr != nil {
		tunnelErr = fmt.Errorf("tunnel cleanup failed; state retained for recovery: %w", tunnelErr)
	}
	return errors.Join(err, serverErr, tunnelErr)
}
