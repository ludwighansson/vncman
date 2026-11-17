// Command vncman lists the VMs running on a libvirt/KVM node and opens their
// VNC consoles in the browser or a VNC viewer. See docs/adr for the design.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ludwighansson/vncman/internal/config"
	"github.com/ludwighansson/vncman/internal/console"
	"github.com/ludwighansson/vncman/internal/platform"
	"github.com/ludwighansson/vncman/internal/ssh"
	"github.com/ludwighansson/vncman/internal/tui"
	"github.com/ludwighansson/vncman/internal/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// fullVersion falls back to the module version that `go install` records.
func fullVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return version
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "vncman:", err)
		os.Exit(1)
	}
}

func run() error {
	defaultConfig, err := config.DefaultPath()
	if err != nil {
		return err
	}
	configPath := flag.String("config", defaultConfig, "path to the config file")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintln(out, "Usage: vncman [flags] [node]")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Lists the VMs running on a libvirt/KVM node and opens their VNC consoles in")
		fmt.Fprintln(out, "the browser or a VNC viewer. node is any SSH destination, including")
		fmt.Fprintln(out, "~/.ssh/config aliases.")
		fmt.Fprintln(out, "Without a node, vncman starts with a list of recent and configured nodes.")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Flags:")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println("vncman", fullVersion())
		return nil
	}
	if flag.NArg() > 1 {
		flag.Usage()
		return errors.New("expected at most one node")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	stateDir, err := platform.StateDir()
	if err != nil {
		return err
	}
	statePath := filepath.Join(stateDir, "state.json")
	state, err := config.LoadState(statePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vncman: ignoring saved state: %v\n", err)
		state = config.State{}
	}
	cacheDir, err := platform.CacheDir()
	if err != nil {
		return err
	}
	controlDir, err := ssh.ControlDir(cacheDir)
	if err != nil {
		return err
	}

	srv, err := web.New(cfg.HTTPPort, console.NoVNC())
	if err != nil {
		return err
	}
	go func() { _ = srv.Serve() }()

	ui := tui.New(tui.Options{
		Config:     cfg,
		State:      &state,
		StatePath:  statePath,
		ControlDir: controlDir,
		Web:        srv,
	})
	defer ui.Close()

	// Connect before the UI takes over the terminal, so ssh can ask for a
	// passphrase or a security-key touch as usual.
	if node := flag.Arg(0); node != "" {
		if err := ui.Connect(node, os.Stdout, os.Stderr); err != nil {
			return err
		}
	}

	p := tea.NewProgram(ui, tea.WithAltScreen())
	ui.SetSend(p.Send)
	_, err = p.Run()
	return err
}
