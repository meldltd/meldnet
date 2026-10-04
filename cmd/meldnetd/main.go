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
	"meldnet/internal/profiles"
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
	_, err = config.Open(absolute)
	if err != nil {
		return err
	}
	lock, err := securefs.Lock(filepath.Join(absolute, "daemon.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()

	var owner *vpn.Owner
	var dns *privatedns.Hub
	if !*simulate {
		owner, err = vpn.NewOwner()
		if err != nil {
			return err
		}
		defer owner.Close()
		dns, err = privatedns.NewHub(absolute)
		if err != nil {
			return fmt.Errorf("recover private DNS: %w", err)
		}
	}
	coordinator := vpn.NewCoordinator()
	factory := func(id, dir string, o *control.Options) (*profiles.Runtime, error) {
		store, e := config.Open(dir)
		if e != nil {
			return nil, e
		}
		var engine vpn.Engine = &vpn.Simulator{}
		closeEngine := func() error { return nil }
		if owner != nil {
			w, e := owner.NewWireGuard(filepath.Join(dir, "runtime"), id)
			if e != nil {
				return nil, e
			}
			engine = w
			closeEngine = w.Close
		}
		wrapped := coordinator.Wrap(id, engine)
		svc, e := service.New(store, wrapped)
		if e != nil {
			return nil, errors.Join(e, closeEngine())
		}
		if dns != nil {
			svc.SetDNS(dns.Slot(id))
		}
		manager, e := control.New(dir, svc, o)
		if e != nil {
			return nil, errors.Join(e, closeEngine())
		}
		return &profiles.Runtime{Service: svc, Manager: manager, Engine: wrapped, Close: closeEngine}, nil
	}
	var options *control.Options
	if *primary {
		options = &control.Options{Listen: *listen, URL: *publicURL, Endpoint: *endpoint, Pool: *pool, Name: *name}
	}
	networks, err := profiles.New(absolute, factory, options)
	if err != nil {
		return err
	}
	defer networks.Close()
	root, _ := networks.Runtime(profiles.Default)
	svc := root.Service
	listener, cleanup, err := api.Listen(*socket, *uid)
	if err != nil {
		return err
	}
	defer cleanup()
	server := &http.Server{Handler: api.ProfilesHandler(networks), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 40 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 8 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result := make(chan error, 2)
	managedCtx, cancelManaged := context.WithCancel(ctx)
	managedDone := make(chan struct{})
	go func() { defer close(managedDone); networks.Run(managedCtx) }()
	go func() { result <- networks.Serve(managedCtx) }()
	go func() { result <- server.Serve(listener) }()
	fmt.Fprintf(os.Stderr, "meldnetd: backend=%s socket=%s allowed_uid=%d\n", svc.Status(context.Background()).Backend, *socket, *uid)
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
	tunnelErr := networks.Close()
	if tunnelErr != nil {
		tunnelErr = fmt.Errorf("tunnel cleanup failed; state retained for recovery: %w", tunnelErr)
	}
	return errors.Join(err, serverErr, tunnelErr)
}
