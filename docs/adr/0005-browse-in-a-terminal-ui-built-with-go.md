# 0005. Browse in a terminal UI built with Go

- Status: Accepted
- Date: 2026-11-16

## Context

VNCMan needs a way to browse the VMs on a node and pick one to connect to. These options were considered:

- a native Mac GUI, which in practice means SwiftUI
- a web page in the browser, served by a local process (option A)
- the same web page in an app window, built with Wails (option B)
- a terminal UI (TUI)

Go is the preferred language. It produces a single binary that is easy to share, and its standard library covers HTTP, XML and process management. Go has no native macOS UI toolkit, which pushes a native GUI toward Swift, and Swift isn't wanted.

The users are operators who already work in a terminal with SSH. A TUI is the quickest to build, and it runs where ssh can prompt ([0002](0002-use-the-system-openssh-client.md)). A local web server is needed anyway for browser consoles ([0004](0004-open-consoles-in-the-browser-with-novnc.md)), which leaves options A and B as natural next steps.

## Decision

We will build v1 as a Go program, `vncman`, with a terminal UI built on Bubble Tea, using Bubbles for the list and filter and Lip Gloss for styling.

- **Usage.** `vncman <node>` opens a node directly. `vncman` with no argument lists configured and recent nodes to choose from.
- **Screen.**
  - A header shows the node and the number of running VMs.
  - A filterable table shows each VM's display name, project, flavor, libvirt name, UUID and VNC port. Every listed VM is running, so there is no state column. The filter matches every column, so an instance UUID pasted from a ticket finds the VM. Columns with no data on the current node are hidden, such as project and flavor on nodes without Nova.
  - A footer lists the keys.
  - When sudo on the node needs a password, a masked password field appears ([0001](0001-discover-vms-with-read-only-virsh-over-ssh.md)).
- **Keys.**
  - ↑ and ↓ move through the list.
  - `/` filters it.
  - Enter opens the browser console, and `t` opens TigerVNC.
  - `r` refreshes, and `n` switches node.
  - `q` quits, and asks first if any console is open.
  - A marker shows which VMs have an open console.
- **Code layout.** The core is UI-agnostic so that later front ends can reuse it. Only `internal/tui` imports Bubble Tea.

  ```
  cmd/vncman/         entry point and flags
  internal/libvirt/   virsh over SSH and domain XML parsing        (0001)
  internal/ssh/       ControlMaster lifecycle and command building (0002)
  internal/relay/     ssh -W relay with TCP and WebSocket entry    (0003)
  internal/console/   embedded noVNC and the TigerVNC launcher     (0004)
  internal/web/       local HTTP server and access control         (0006)
  internal/config/    config file and app state                    (0007)
  internal/tui/       Bubble Tea UI
  ```

**Roadmap.** These steps are not part of v1, and each gets its own ADR when it starts:

1. **A web page for browsing (option A).** The local web server also serves the VM list, and `vncman --web` opens it. This needs an access model beyond per-console tokens ([0006](0006-lock-down-the-local-web-server.md)).
2. **An app window (option B).** The web page runs inside a Wails app with a Dock icon. Because it doesn't start from a terminal, it needs askpass prompts and environment resolution ([0002](0002-use-the-system-openssh-client.md)).

## Consequences

- v1 is the smallest thing that works: one static binary, installed with `go install` or a Homebrew tap. It needs no app bundle, code signing or Gatekeeper exceptions.
- It is keyboard-driven and fast to use for people who live in a terminal.
- Options A and B reuse the core packages and the web server instead of starting over.
- v1 is not a GUI app. It has no Dock icon, it must be started from a terminal, and that terminal stays open while consoles are open, because VNCMan is the relay ([0003](0003-relay-vnc-over-ssh-channels.md)).
- v1 depends on Bubble Tea, Bubbles and Lip Gloss.

## Alternatives considered

- **A SwiftUI app.** It would be the most native option, but it means Swift instead of Go, plus a second stack for the later web work.
- **Wails first (option B).** It gives a native window, but brings app bundling and the no-terminal SSH problems in from the start. Deferred, not rejected.
- **A web page first (option A).** The TUI was preferred as a starting point. Since v1 already builds the web server for consoles, this option stays cheap to add later.
- **Fyne, Gio or darwinkit.** Fyne and Gio draw their own widgets, so they don't look native on macOS. darwinkit is native but a small project with maintenance risk.
- **A plain prompt or `fzf` picker.** It is simpler, but has no live state, no in-place refresh and no open-console markers, and `fzf` would be an extra dependency.
