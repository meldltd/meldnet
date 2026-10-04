package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
	"meldnet/internal/api"
	"meldnet/internal/config"
	"meldnet/internal/profiles"
	"meldnet/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "meldnet:", err)
		os.Exit(1)
	}
}
func run() error {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return errors.New("supported platforms are macOS and Linux")
	}
	networkID := flag.String("network", "default", "network profile to manage (before subcommand)")
	socket := flag.String("socket", api.DefaultSocket, "daemon control socket (before subcommand)")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: meldnet [--socket PATH] [--network NAME] [tui|networks|autoconnect on|off|status|init|config|apply FILE|key|up|down|invite|join|peers|revoke NAME]\n\nNo command opens the TUI. config exports editable settings; apply checks revision.")
		flag.PrintDefaults()
	}
	flag.Parse()
	c := api.NewClient(*socket).ForNetwork(*networkID)
	defer c.Close()
	args := flag.Args()
	if len(args) == 0 {
		args = []string{"tui"}
	}
	ctx := context.Background()
	if args[0] != "init" && args[0] != "apply" && args[0] != "join" && args[0] != "revoke" && args[0] != "autoconnect" && len(args) != 1 {
		return errors.New("unexpected arguments")
	}
	switch args[0] {
	case "networks":
		result, err := c.Networks(ctx)
		if err != nil {
			return err
		}
		return output(result)
	case "autoconnect":
		if len(args) != 2 || (args[1] != "on" && args[1] != "off") {
			return errors.New("usage: meldnet --network NAME autoconnect on|off")
		}
		return c.SetAutoConnect(ctx, args[1] == "on")
	case "invite":
		key, e := c.Invite(ctx)
		if e != nil {
			return e
		}
		fmt.Println(key)
		return nil
	case "peers":
		n, e := c.Network(ctx)
		if e != nil {
			return e
		}
		return output(n)
	case "revoke":
		if len(args) != 2 {
			return errors.New("usage: meldnet revoke NAME")
		}
		return c.Revoke(ctx, args[1])
	case "join":
		f := flag.NewFlagSet("join", flag.ContinueOnError)
		name := f.String("name", "", "device name")
		auto := f.Bool("auto-connect", true, "connect this network at daemon startup")
		connect := f.Bool("connect", true, "connect immediately after enrollment")
		file := f.String("key-file", "-", "enrollment key file, or - for stdin")
		if e := f.Parse(args[1:]); e != nil {
			return e
		}
		if f.NArg() != 0 {
			return errors.New("unexpected join arguments")
		}
		var reader io.Reader = os.Stdin
		if *file != "-" {
			in, e := os.Open(*file)
			if e != nil {
				return e
			}
			defer in.Close()
			reader = in
		}
		b, e := io.ReadAll(io.LimitReader(reader, 8193))
		if e != nil {
			return e
		}
		if len(b) > 8192 {
			return errors.New("enrollment key too long")
		}
		return c.JoinNetwork(ctx, profiles.JoinRequest{ID: *networkID, Name: *name, Key: strings.TrimSpace(string(b)), AutoConnect: *auto, Connect: *connect})
	case "tui":
		_, err := tea.NewProgram(tui.New(c)).Run()
		return err
	case "status":
		s, err := c.Status(ctx)
		if err != nil {
			return err
		}
		return output(s)
	case "config", "key":
		s, err := c.Status(ctx)
		if err != nil {
			return err
		}
		if s.Node == nil {
			return errors.New("initialize this node first")
		}
		if args[0] == "key" {
			fmt.Println(s.Node.PublicKey)
			return nil
		}
		return output(api.ConfigRequest{Revision: s.Node.Revision, Settings: s.Node.Settings})
	case "init":
		f := flag.NewFlagSet("init", flag.ContinueOnError)
		name := f.String("name", "", "node name")
		addresses := f.String("addresses", "", "comma-separated interface CIDRs, e.g. 10.77.0.1/24")
		port := f.Int("port", 51820, "UDP listen port (0 chooses automatically)")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return errors.New("unexpected init arguments")
		}
		p, err := c.Configure(ctx, config.Settings{Name: *name, Addresses: split(*addresses), ListenPort: *port, Peers: []config.Peer{}}, 0)
		if err != nil {
			return err
		}
		return output(p)
	case "apply":
		if len(args) != 2 {
			return errors.New("usage: meldnet apply FILE (or - for stdin)")
		}
		var reader io.Reader = os.Stdin
		if args[1] != "-" {
			f, err := os.Open(args[1])
			if err != nil {
				return err
			}
			defer f.Close()
			reader = f
		}
		var req api.ConfigRequest
		dec := json.NewDecoder(io.LimitReader(reader, 128<<10))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			return errors.New("invalid settings JSON; use meldnet config to export an editable document")
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			return errors.New("unexpected trailing JSON")
		}
		p, err := c.Configure(ctx, req.Settings, req.Revision)
		if err != nil {
			return err
		}
		return output(p)
	case "up":
		return c.Connect(ctx)
	case "down":
		return c.Disconnect(ctx)
	default:
		flag.Usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}
func output(value any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}
func split(value string) []string {
	var values []string
	for _, s := range strings.Split(value, ",") {
		if v := strings.TrimSpace(s); v != "" {
			values = append(values, v)
		}
	}
	return values
}
