# 0008. Support Linux

- Status: Accepted
- Date: 2026-11-17

## Context

VNCMan was built for macOS, but many operators work on Linux. There, virt-viewer can open a VM's console. However, it shows libvirt names instead of Nova instance names and has no filter. It also needs the libvirt client and libvirt group membership instead of handling sudo, and it has no browser consoles.

Most of VNCMan doesn't depend on macOS: the system ssh, virsh on the node, the relay, the local web server, the embedded noVNC and the Bubble Tea UI. It already compiles for Linux unchanged. Three pieces are specific to macOS:

- the browser is opened with `open`
- TigerVNC is found through Spotlight and its `.app` bundle ([0004](0004-open-consoles-in-the-browser-with-novnc.md)). [0009](0009-let-users-choose-the-vnc-viewer.md) replaces this.
- the state file lives in `~/Library/Application Support` and the ssh control sockets in `~/Library/Caches`. On Linux, Go's directory helpers would put the state file next to the config file.

## Decision

We will support macOS and Linux from the same code.

- **Keep platform code in one place.** The few OS-specific functions live in an `internal/platform` package, with one file per OS chosen by build tags. `platform_darwin.go` covers macOS. `platform_unix.go` covers Linux and other Unix systems, following the XDG Base Directory conventions. Everything else stays independent of the OS.
- **Open the browser with the OS's tool.** macOS uses `open` and Linux uses `xdg-open`. If the browser can't be opened, the status line shows the console URL so the user can copy it.
- **Use each OS's file locations.**

  | | macOS | Linux |
  |---|---|---|
  | Config | `~/.config/vncman/config.toml` | the same; both honour `$XDG_CONFIG_HOME` |
  | State | `~/Library/Application Support/vncman/state.json` | `$XDG_STATE_HOME/vncman/state.json`, by default `~/.local/state/vncman/state.json` |
  | ssh control sockets | `~/Library/Caches/vncman/cm` | `$XDG_CACHE_HOME/vncman/cm`, by default `~/.cache/vncman/cm` |

- **Don't support Windows.** Windows' OpenSSH does not implement `ControlMaster`, which [0002](0002-use-the-system-openssh-client.md) relies on.
- **Test on both systems.** The unit tests run on macOS and Linux. The end-to-end check with a fake node (fake `ssh`, `virsh` and VNC server) moves into the repository, so it can run on both systems in CI once the repository has CI.
- **Build release binaries** for macOS (arm64, amd64) and Linux (amd64, arm64) with `CGO_ENABLED=0`, so they don't depend on system libraries. How they are published, for example as GitHub releases, a Homebrew tap or distribution packages, is a separate decision.

## Consequences

- Operators get the same tool on both kinds of workstation, including Nova names, sudo handling and browser consoles on Linux.
- Every change has to work on two systems, and the tests run on both.
- On Linux, browser consoles need `xdg-open`. It is part of xdg-utils, which desktop distributions install.

## Alternatives considered

- **Recommend virt-viewer on Linux.** It has no Nova names, filter, sudo handling or browser consoles, and it needs the libvirt client on every workstation.
- **Write a separate Linux tool.** This would duplicate everything for three small differences.
- **Check `runtime.GOOS` throughout the code.** This works, but it scatters platform differences around the code. One package with a file per OS is easier to review and test.
- **Put the control sockets in `$XDG_RUNTIME_DIR` on Linux.** It is the XDG location for sockets, but it isn't set in every session, for example after `su`. The cache directory works everywhere.
