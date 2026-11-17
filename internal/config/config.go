// Package config loads VNCMan's configuration file (ADR 0007).
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/ludwighansson/vncman/internal/ssh"
	"github.com/ludwighansson/vncman/internal/viewer"
)

// What Enter does in the VM list.
const (
	ConsoleBrowser = "browser"
	ConsoleViewer  = "viewer"
)

// How virsh is run on a node.
const (
	SudoAuto   = "auto"
	SudoAlways = "always"
	SudoNever  = "never"
)

// Defaults used when the config file does not set a value.
const (
	DefaultVirsh          = "virsh -r -c qemu:///system"
	DefaultControlPersist = "10m"
)

// Config is the contents of config.toml.
type Config struct {
	Console  string `toml:"console"`
	Viewer   Argv   `toml:"viewer"`
	HTTPPort int    `toml:"http_port"`
	Sudo     string `toml:"sudo"`
	Virsh    string `toml:"virsh"`
	SSH      SSH    `toml:"ssh"`
	Nodes    []Node `toml:"nodes"`
}

// Argv is a command given as a list of arguments, such as the viewer.
type Argv []string

// UnmarshalTOML accepts only a list of strings. A single command string is
// rejected with an example, because VNCMan never runs commands through a
// shell (ADR 0009).
func (a *Argv) UnmarshalTOML(v any) error {
	list, ok := v.([]any)
	if !ok {
		return errors.New(`viewer must be a list of arguments, for example viewer = ["remmina", "-c", "vnc://{host}:{port}"]`)
	}
	out := make(Argv, len(list))
	for i, item := range list {
		s, ok := item.(string)
		if !ok {
			return fmt.Errorf("viewer: argument %d must be a string", i+1)
		}
		out[i] = s
	}
	*a = out
	return nil
}

// SSH holds settings for the ssh client.
type SSH struct {
	ControlPersist string `toml:"control_persist"`
}

// Node is a node listed in the config file. Empty fields inherit the
// global value.
type Node struct {
	Name  string `toml:"name"`
	Group string `toml:"group"`
	Sudo  string `toml:"sudo"`
	Virsh string `toml:"virsh"`
}

// NodeSettings are the effective settings for one node.
type NodeSettings struct {
	Sudo  string
	Virsh string
}

// Default returns the configuration used when there is no config file.
func Default() Config {
	return Config{
		Console: ConsoleBrowser,
		Viewer:  append(Argv{}, viewer.Default...),
		Sudo:    SudoAuto,
		Virsh:   DefaultVirsh,
		SSH:     SSH{ControlPersist: DefaultControlPersist},
	}
}

// DefaultPath returns $XDG_CONFIG_HOME/vncman/config.toml, falling back to
// ~/.config/vncman/config.toml.
func DefaultPath() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "vncman", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "vncman", "config.toml"), nil
}

// Load reads the config file at path. A missing file yields the defaults.
func Load(path string) (Config, error) {
	cfg := Default()
	md, err := toml.DecodeFile(path, &cfg)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("reading %s: %w", path, err)
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		return Config{}, fmt.Errorf("%s: unknown setting %q", path, keys[0].String())
	}
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func (c Config) validate() error {
	switch c.Console {
	case ConsoleBrowser, ConsoleViewer:
	default:
		return fmt.Errorf("console must be %q or %q, not %q", ConsoleBrowser, ConsoleViewer, c.Console)
	}
	if err := viewer.Validate(c.Viewer); err != nil {
		return err
	}
	if c.HTTPPort < 0 || c.HTTPPort > 65535 {
		return fmt.Errorf("http_port %d is out of range", c.HTTPPort)
	}
	if err := validateSudo(c.Sudo); err != nil {
		return err
	}
	if strings.TrimSpace(c.Virsh) == "" {
		return errors.New("virsh must not be empty")
	}
	if strings.TrimSpace(c.SSH.ControlPersist) == "" {
		return errors.New("ssh.control_persist must not be empty")
	}
	seen := make(map[string]bool)
	for i, n := range c.Nodes {
		if err := ssh.ValidateDestination(n.Name); err != nil {
			return fmt.Errorf("nodes[%d]: %w", i, err)
		}
		if seen[n.Name] {
			return fmt.Errorf("node %q is listed twice", n.Name)
		}
		seen[n.Name] = true
		if n.Sudo != "" {
			if err := validateSudo(n.Sudo); err != nil {
				return fmt.Errorf("node %q: %w", n.Name, err)
			}
		}
	}
	return nil
}

func validateSudo(s string) error {
	switch s {
	case SudoAuto, SudoAlways, SudoNever:
		return nil
	}
	return fmt.Errorf("sudo must be %q, %q or %q, not %q", SudoAuto, SudoAlways, SudoNever, s)
}

// ForNode returns the effective settings for the named node.
func (c Config) ForNode(name string) NodeSettings {
	s := NodeSettings{Sudo: c.Sudo, Virsh: c.Virsh}
	for _, n := range c.Nodes {
		if n.Name != name {
			continue
		}
		if n.Sudo != "" {
			s.Sudo = n.Sudo
		}
		if n.Virsh != "" {
			s.Virsh = n.Virsh
		}
		break
	}
	return s
}

// Group returns the configured group of the named node, if any.
func (c Config) Group(name string) string {
	for _, n := range c.Nodes {
		if n.Name == name {
			return n.Group
		}
	}
	return ""
}
