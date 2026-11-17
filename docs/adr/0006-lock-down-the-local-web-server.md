# 0006. Lock down the local web server

- Status: Accepted
- Date: 2026-11-16

## Context

Browser consoles need an HTTP and WebSocket server on the Mac ([0003](0003-relay-vnc-over-ssh-channels.md), [0004](0004-open-consoles-in-the-browser-with-novnc.md)). That server opens SSH channels to production hypervisors as the user, so nobody but the user may drive it. The threats are:

- **Other websites open in the user's browser.** Browsers let any page send requests to `127.0.0.1`, and WebSocket connections are not subject to CORS.
- **DNS rebinding.** An attacker's hostname resolves to `127.0.0.1`, which lets an attacker's page reach the local server under its own origin.
- **Other local users on the same Mac.**

## Decision

- **Bind to `127.0.0.1` only.** By default the server takes a random free port on each run. The `http_port` setting can fix it ([0007](0007-keep-configuration-in-a-toml-file.md)).
- **Give each console its own token.** When the user presses Enter, VNCMan creates a random 128-bit token. It is valid in two URLs: `/console/<token>`, the noVNC page, and `/ws/<token>`, its WebSocket.
  - A token maps to exactly one node and domain UUID. Unknown tokens get a 404.
  - Tokens are kept in memory only. A token stays valid while VNCMan runs, so noVNC can reconnect. It is revoked when the user closes the console in the UI or quits VNCMan.
- **Check `Host` on every request.** It must be exactly `127.0.0.1:<port>`, which defeats DNS rebinding.
- **Check `Origin` on WebSocket upgrades.** It must be exactly `http://127.0.0.1:<port>`.
- **Accept no node or VM from the browser.** No endpoint lists nodes or VMs, or takes a node or domain as input. The browser can only open consoles that the TUI created.
- **Serve static assets without a token.** The noVNC files under `/novnc/` are not sensitive.
- **Send protective headers.**
  - `Cache-Control: no-store`
  - `Referrer-Policy: no-referrer`, which keeps tokens out of referrers
  - `Content-Security-Policy: frame-ancestors 'none'`, which stops other pages from embedding consoles
- **Use plain HTTP.** Traffic never leaves the machine, so there is no TLS.

## Consequences

- A web page in the user's browser cannot open consoles or reach nodes, because it cannot learn a token.
- Other local users cannot use the web server without a token. The TCP entry point for TigerVNC remains unauthenticated, as noted in [0003](0003-relay-vnc-over-ssh-channels.md).
- Tokens end up in browser history, but they are useless once VNCMan exits.
- Console URLs change between runs, so bookmarks don't carry over. This is intended.
- The web page for browsing on the roadmap ([0005](0005-browse-in-a-terminal-ui-built-with-go.md)) will need endpoints that list VMs. Its access model gets its own ADR.

## Alternatives considered

- **Loopback binding with no token.** Any website could open WebSockets to the server, so this is rejected.
- **Serving HTTP over a UNIX socket.** Browsers cannot connect to one.
- **TLS with a self-signed certificate.** It causes certificate warnings and adds nothing on loopback.
- **Cookie-based sessions.** They need a login step, while a token URL is simpler for a single-user tool.
