# Architecture Decision Records

VNCMan lists the virtual machines running on a libvirt/KVM node and opens a VNC console to the one you pick, from macOS or Linux. It does what `virt-viewer --connect qemu+ssh://<node>/system` does on Linux. You browse VMs in a terminal UI, and the console opens in a browser tab or in the VNC viewer of your choice.

VNCMan was called vnc-m until 2026-11-17.

This directory records the decisions behind the design. Each record gives the context, the decision, its consequences and the alternatives considered, following Michael Nygard's ADR format.

## How it fits together

```
 Mac or Linux                                              node (libvirt/KVM)
┌─ VNCMan: Go, runs in your terminal ────────┐
│                                            │  ssh, one ControlMaster per node (0002)
│  TUI: VM list, filter, Enter       (0005)  │── virsh -r dumpxml ───────▶  libvirtd  (0001)
│  relay                             (0003)  │── ssh -W <listen>:<port> ─▶  QEMU VNC
│   ├─ WebSocket on 127.0.0.1        (0006)  │
│   └─ TCP on 127.0.0.1                      │
└──────┬─────────────────────┬───────────────┘
       │                     │
       ▼                     ▼
  browser tab: noVNC    VNC viewer (v key)                  (0004, 0009)
```

## Roadmap

1. **v1:** browse in a terminal UI and open consoles in the browser (this set of ADRs).
2. **Later:** a web page for browsing, served by the same local server.
3. **Later:** an app window with a Dock icon, wrapping that web page with Wails.

See [0005](0005-browse-in-a-terminal-ui-built-with-go.md) for what each later step involves.

## Index

| ADR | Title | Status |
|-----|-------|--------|
| [0001](0001-discover-vms-with-read-only-virsh-over-ssh.md) | Discover VMs with read-only `virsh` over SSH | Accepted |
| [0002](0002-use-the-system-openssh-client.md) | Use the system OpenSSH client | Accepted |
| [0003](0003-relay-vnc-over-ssh-channels.md) | Relay VNC over SSH channels | Accepted, partly superseded by 0009 |
| [0004](0004-open-consoles-in-the-browser-with-novnc.md) | Open consoles in the browser with noVNC | Accepted, partly superseded by 0009 |
| [0005](0005-browse-in-a-terminal-ui-built-with-go.md) | Browse in a terminal UI built with Go | Accepted |
| [0006](0006-lock-down-the-local-web-server.md) | Lock down the local web server | Accepted |
| [0007](0007-keep-configuration-in-a-toml-file.md) | Keep configuration in a TOML file | Accepted, partly superseded by 0008 and 0009 |
| [0008](0008-support-linux.md) | Support Linux | Accepted |
| [0009](0009-let-users-choose-the-vnc-viewer.md) | Let users choose the VNC viewer | Accepted |
| [0010](0010-publish-releases-with-goreleaser.md) | Publish releases with GoReleaser | Accepted |

## Process

- Copy [template.md](template.md) to the next free number as `NNNN-short-title.md`.
- Start every record at **Proposed**. Move it to **Accepted** once agreed, or to **Rejected** with a note explaining why.
- Don't rewrite an accepted decision. Write a new ADR that supersedes it, and set the old one's status to **Superseded by NNNN**.
