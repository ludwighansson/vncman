# 0004. Open consoles in the browser with noVNC

- Status: Accepted, partly superseded by [0009](0009-let-users-choose-the-vnc-viewer.md)
- Date: 2026-11-16

## Context

Rendering a VNC session well is a large, solved problem: RFB encodings, keyboard mapping, clipboard and scaling. VNCMan should not implement it. The candidates on a Mac are:

- **noVNC**, a JavaScript VNC client and the console behind OpenStack Horizon (MPL-2.0)
- **TigerVNC**, the `vncviewer` many operators use on Linux, available as a native Mac app
- **macOS Screen Sharing**, which is built in and opened with `vnc://` URLs. It has a history of trouble with QEMU VNC servers that have no password, which would need re-checking on current macOS.
- **RealVNC Viewer**, which is proprietary

The chosen workflow is to browse in the terminal ([0005](0005-browse-in-a-terminal-ui-built-with-go.md)) and have the console open in a browser tab, with a native viewer available when needed.

## Decision

- **Enter opens the console in a new browser tab, using noVNC.**
  - VNCMan embeds a pinned noVNC release in its binary with `embed.FS` and serves it from the local web server ([0006](0006-lock-down-the-local-web-server.md)). It needs no CDN and works offline.
  - It uses noVNC's full client page (`vnc.html`) with `autoconnect`, `resize=scale`, and a `path` that points to the console's WebSocket ([0003](0003-relay-vnc-over-ssh-channels.md)). That page already provides Ctrl-Alt-Del, clipboard, fullscreen and scaling controls.
  - The tab title shows the VM's display name and the node.
  - VNCMan opens the URL in the default browser with `open`.
- **`t` opens the console in TigerVNC instead,** through the TCP relay ([0003](0003-relay-vnc-over-ssh-channels.md)).
  - VNCMan finds TigerVNC through Launch Services by its bundle identifier, `com.tigervnc.tigervnc`.
  - It runs the `vncviewer` binary inside the bundle with `127.0.0.1::<port>`, where `::` means a raw TCP port rather than a display number.
  - VNCMan tracks the viewer process and closes the relay when it exits.
  - If TigerVNC is missing, the UI says so and shows the install command, `brew install --cask tigervnc-viewer`.
- **The `console` setting chooses what Enter does:** `"browser"` (the default) or `"tigervnc"` ([0007](0007-keep-configuration-in-a-toml-file.md)).

## Consequences

- The default path needs nothing installed. Each console gets its own tab, and operators already know noVNC from Horizon.
- TigerVNC is one key away when the browser falls short.
- The browser keeps some shortcuts, such as ⌘W, ⌘T and ⌘Q, so they never reach the VM. noVNC's on-screen keys cover Ctrl-Alt-Del and similar combinations. Users who need full keyboard control use `t`.
- VNCMan ships a copy of noVNC. Its MPL-2.0 license notice must be included, and updates are a deliberate version bump.
- VNC servers that require authentication are out of scope for v1. That covers a VNC password, or the VeNCrypt/TLS client certificates that Nova can be configured to require.

## Alternatives considered

- **TigerVNC as the default.** It has better keyboard handling and rendering, but it is a separate app to install, and opening consoles in the browser was preferred.
- **Screen Sharing (`open vnc://…`).** It needs no install, but its compatibility with password-less QEMU servers is doubtful. It could be added as an option after testing.
- **Loading noVNC from a CDN.** This keeps the binary smaller, but it needs network access and puts a third party in the console's path.
- **Writing a VNC client.** It would give the most control for the most work, which isn't justified.
