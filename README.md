# VNCMan

Browse the VMs running on a libvirt/KVM node from your terminal, and open their VNC consoles in the browser or in the VNC viewer you prefer. It works on macOS and Linux, and does what `virt-viewer --connect qemu+ssh://<node>/system` does, with OpenStack instance names, filtering and sudo handling on top.

```
VNCMan · node1 · 12 running
     Name             UUID                                  VNC
 ──────────────────────────────────────────────────────────────
     db01             0b8f3c52-1d6e-4a8b-9a51-3f2c7e9d4a10  5900
 ●   web01            7c1e9a44-52b3-4f0e-8d2a-6e5b1c3f9d27  5903
     ...
↑↓ move · / filter · ⏎ browser · v viewer · r refresh · n node · q quit
```

## Install

**From a release:** download the archive for your system from the [releases page](https://github.com/ludwighansson/vncman/releases), unpack it, and put `vncman` on your `PATH`:

```sh
tar -xzf vncman_*_darwin_arm64.tar.gz
sudo mv vncman /usr/local/bin/
```

The macOS binaries are not signed. If you downloaded the archive with a browser, macOS blocks the first run. Remove the quarantine with `xattr -d com.apple.quarantine /usr/local/bin/vncman`.

**With Go** (1.24 or later):

```sh
go install github.com/ludwighansson/vncman/cmd/vncman@latest
```

**From a checkout:** `go build -o vncman ./cmd/vncman`

noVNC is built in, so the browser console needs nothing else. For the `v` key, install a VNC viewer: TigerVNC is the default (`brew install --cask tigervnc-viewer` on macOS, the `tigervnc-viewer` package on Debian and Ubuntu), or [choose your own](#choose-a-vnc-viewer).

## Use

```sh
vncman node1        # any SSH destination, including ~/.ssh/config aliases
vncman              # pick from recent and configured nodes
```

| Key | Action |
|---|---|
| ↑ ↓, PgUp PgDn | Move |
| `/` | Filter by name, project, flavor or UUID. Enter keeps the filter, Esc clears it. |
| Enter | Open the console in a new browser tab (noVNC) |
| `v` | Open the console in your VNC viewer |
| `r` | Refresh the list (reconnects if the SSH connection has closed) |
| `n` | Switch to another node |
| `q` | Quit. Asks first if consoles are connected, because quitting closes them. |

A `●` marks VMs with a connected console. Keep VNCMan running while you use a console: the console's traffic goes through it.

## How it works

- **SSH.** VNCMan uses your normal `ssh`, so `~/.ssh/config`, your agent, bastions and security keys work as they do in the terminal. You log in once per node. Any prompts (passphrase, host key, key touch) appear in the terminal before the UI starts. Every later command reuses that connection and never prompts.
- **Listing VMs.** Each refresh runs `virsh -r -c qemu:///system list --uuid --state-running`, then one `virsh … 'dumpxml …; dumpxml …'` for all running VMs. The connection is always read-only (`-r`).
- **sudo.** By default VNCMan tries `virsh` without sudo first, then `sudo -n`. If sudo needs a password, the UI asks for it once per node. The password is passed to sudo on standard input, kept in memory until VNCMan quits, and never written to disk.
- **Consoles.** Each connection looks the VM's VNC port up again, then tunnels to it with `ssh -W` over the existing connection.
  - The browser console is served on `127.0.0.1`, behind a random per-console token, with Host and Origin checks.
  - A viewer connects to a port on `127.0.0.1`. The port closes 30 seconds after its last connection ends, or after 60 seconds if no viewer connects.

The reasoning behind each choice is in [docs/adr](docs/adr/README.md).

## Choose a VNC viewer

Set `viewer` in the config file to the command that opens your viewer, as a list of arguments. VNCMan replaces these placeholders:

| Placeholder | Value |
|---|---|
| `{host}` | `127.0.0.1` |
| `{port}` | the port VNCMan relays to the VM |
| `{title}` | the VM's name and node, for example `web01 · node1` |

The command is run directly, never through a shell, so VM names can't inject commands. Each placeholder stays within its argument.

The default is TigerVNC:

```toml
viewer = ["vncviewer", "{host}::{port}"]   # "::" means a TCP port, not a display number
```

On macOS, VNCMan also finds `vncviewer` inside TigerVNC.app when it isn't on your `PATH`.

## Configure

Configuration is optional. VNCMan reads `~/.config/vncman/config.toml`, or `$XDG_CONFIG_HOME/vncman/config.toml` if that is set:

```toml
# What Enter does: "browser" (the default) or "viewer"
console = "browser"

# The viewer that v opens; see "Choose a VNC viewer"
viewer = ["vncviewer", "{host}::{port}"]

# Port for the local web server. 0 picks a free port on each run (the default).
http_port = 0

# "auto" (the default) tries without sudo, then with sudo, then asks for the password.
# "always" always uses sudo; "never" never does.
sudo = "auto"

# Remote command used to query libvirt.
virsh = "virsh -r -c qemu:///system"

[ssh]
control_persist = "10m"

[[nodes]]
name = "compute-01"          # any SSH destination
group = "region-a"

[[nodes]]
name = "kolla-compute-07"
virsh = "docker exec nova_libvirt virsh -r"
```

VNCMan keeps a little state of its own:

| | macOS | Linux |
|---|---|---|
| Recent nodes | `~/Library/Application Support/vncman/state.json` | `~/.local/state/vncman/state.json` |
| SSH control sockets | `~/Library/Caches/vncman/cm` | `~/.cache/vncman/cm` |

On Linux, these honour `$XDG_STATE_HOME` and `$XDG_CACHE_HOME`.

## Requirements on the nodes

- `virsh`, or a command that runs it (see `virsh` above).
- Read access to libvirt through its read-only socket, sudo, or membership of the `libvirt` group. sudoers must not set `requiretty`.
- `AllowTcpForwarding` in sshd (the default), for the console tunnel.
- VNC servers without authentication, on a TCP port. VMs with SPICE only or VNC on a UNIX socket are listed but can't be opened.

## Development

```sh
go test -race ./...          # unit tests
python3 test/e2e/run.py      # end-to-end check against a fake node; needs go, python3 and nc
```

The end-to-end check runs the real binary in a pseudo-terminal. It uses fake `ssh`, `virsh`, browser opener and viewer commands, plus a fake VNC server. It works on macOS and Linux and never touches your real home directory.

noVNC (MPL-2.0, see `internal/console/novnc/LICENSE.txt`) is embedded from `internal/console/novnc`. Update it with `scripts/update-novnc.sh <tag>`. Its `vendor/` directory is stored as `third_party/`, because Go module zips (and so `go install`) leave out vendor directories.

Releases are built with [GoReleaser](https://goreleaser.com) from `.goreleaser.yaml`. Pushing a `vX.Y.Z` tag runs the release workflow, which publishes a GitHub release:

```sh
git tag v0.1.0 && git push origin v0.1.0
```

To test a release locally without publishing, run `goreleaser release --snapshot --clean`.

## License

VNCMan is released under the [MIT License](LICENSE). It embeds [noVNC](https://github.com/novnc/noVNC), which is licensed under the MPL-2.0.
