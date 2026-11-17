# 0007. Keep configuration in a TOML file

- Status: Accepted, partly superseded by [0008](0008-support-linux.md) and [0009](0009-let-users-choose-the-vnc-viewer.md)
- Date: 2026-11-16

## Context

VNCMan needs these settings:

- what Enter does: open the console in the browser or in TigerVNC ([0004](0004-open-consoles-in-the-browser-with-novnc.md))
- whether to use sudo, and the remote `virsh` command, both globally and per node ([0001](0001-discover-vms-with-read-only-virsh-over-ssh.md))
- the SSH `ControlPersist` time ([0002](0002-use-the-system-openssh-client.md))
- the local web server's port ([0006](0006-lock-down-the-local-web-server.md))
- a list of known nodes, optionally grouped, for example by region

Most of these are set once by operators who keep dotfiles. Recent nodes are app state, not configuration. Connection details for nodes, such as user, port and bastion, already live in `~/.ssh/config`.

## Decision

- **Config file.** Configuration lives in `~/.config/vncman/config.toml`, or `$XDG_CONFIG_HOME/vncman/config.toml` if that variable is set. Users edit it by hand, and VNCMan reads it at startup.
- **Read-only config.** VNCMan never writes the config file.
- **State file.** State, such as recent nodes, lives in `~/Library/Application Support/vncman/state.json`. VNCMan owns it.
- **No config required.** Without a config file, VNCMan uses defaults and the user passes a node name.
- **Nodes are SSH destinations.** A node `name` is whatever `ssh <name>` accepts, including `~/.ssh/config` aliases. VNCMan does not duplicate users, ports or jump hosts.

Example:

```toml
# What Enter does: "browser" (noVNC in a new tab, the default) or "tigervnc"
console = "browser"

# Port for the local web server. 0 picks a free port on each run (the default).
http_port = 0

# How to run virsh on the node:
#   "auto"   (default) try without sudo, then with sudo, then ask for the sudo password
#   "always" always use sudo
#   "never"  never use sudo
# A sudo password is asked for once per node and kept in memory until VNCMan quits.
sudo = "auto"

# Remote command used to query libvirt; it runs through the node user's shell.
virsh = "virsh -r -c qemu:///system"

[ssh]
control_persist = "10m"

[[nodes]]
name = "compute-01"          # any SSH destination, including ~/.ssh/config aliases
group = "region-a"

[[nodes]]
name = "kolla-compute-07"
group = "region-b"
virsh = "docker exec nova_libvirt virsh -r"

[[nodes]]
name = "compute-12"
sudo = "always"              # the read-only socket is locked down here
```

## Consequences

- The first run needs no setup. The config fits in dotfiles and can be shared within a team.
- SSH settings stay in one place, `~/.ssh/config`.
- Comments and formatting in the config survive, because VNCMan never rewrites the file.
- Changing a setting means editing a file. v1 has no settings screen.
- VNCMan depends on a TOML parser (`BurntSushi/toml`).

## Alternatives considered

- **UserDefaults or a plist.** This is the Mac convention for apps, but it is awkward to edit by hand or share, and an odd fit for a terminal tool.
- **JSON.** It needs only the standard library, but JSON has no comments.
- **YAML.** It brings indentation pitfalls and a heavier dependency for little gain.
- **A node list from OpenStack** (`openstack hypervisor list`). It would be convenient for Nova deployments, but it is out of scope for v1. It could later become an extra source for the node list.
