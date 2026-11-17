# 0003. Relay VNC over SSH channels

- Status: Accepted, partly superseded by [0009](0009-let-users-choose-the-vnc-viewer.md)
- Date: 2026-11-16

## Context

Each domain's VNC server listens on the node, either on `127.0.0.1` or on the management address. Nova uses the management address when `[vnc] server_listen` is set so that `nova-novncproxy` can reach the server. The port is usually not reachable from a workstation. VNC to QEMU is also typically unauthenticated and unencrypted, so the traffic must go through SSH.

Two kinds of local client need to reach these servers ([0004](0004-open-consoles-in-the-browser-with-novnc.md)):

- noVNC in a browser tab, which speaks VNC over WebSocket
- TigerVNC, which needs a TCP host and port

VNCMan should know which consoles are open, so that it can show them and clean up after them. It must also support several consoles at once.

## Decision

We will relay every console connection through an `ssh -W` channel on the node's master ([0002](0002-use-the-system-openssh-client.md)).

1. For each incoming client stream, VNCMan first reads the domain's VNC port again by UUID ([0001](0001-discover-vms-with-read-only-virsh-over-ssh.md)). With `autoport`, a restarted VM can get a new port, and its old port can go to another VM. Reusing a stale port could show the wrong VM's console.
2. VNCMan then starts `ssh -o ControlPath=<…> -o ControlMaster=no -o BatchMode=yes -W <target>:<vnc port> <node>` and copies bytes in both directions between the client and ssh's stdin and stdout.
3. `<target>` is the domain's VNC listen address. An empty or wildcard address (`0.0.0.0`, `::`) becomes `127.0.0.1`, which `-W` resolves on the node.
4. Two entry points feed the same relay:
   - **WebSocket.** noVNC connects to `ws://127.0.0.1:<http port>/ws/<token>` on VNCMan's local web server ([0006](0006-lock-down-the-local-web-server.md)). Binary WebSocket messages map to the ssh byte stream.
   - **TCP.** For TigerVNC, VNCMan listens on `127.0.0.1:0`, reads back the port the OS assigned, and passes `127.0.0.1::<port>` to the viewer. The listener closes when the viewer process exits, when VNCMan quits, or 30 seconds after its last connection ends. The grace period keeps viewer reconnects working.
5. Nothing binds to an address other than loopback.
6. The relay runs inside the VNCMan process, so quitting VNCMan ends every console. When consoles are open, VNCMan asks for confirmation before it quits.

## Consequences

- The TCP port is known before the viewer starts, so there is no race for a free port.
- Each new connection costs one extra `virsh dumpxml` on the node, in exchange for never connecting to the wrong VM.
- Each console is one SSH channel on an existing master. It needs no new authentication and no long-lived ssh process per idle tunnel.
- VNCMan sees connects and disconnects, so the UI can mark VMs that have an open console.
- VNCMan must keep running while consoles are open.
- The relay needs no sudo on the node. It does need the node's sshd to allow TCP forwarding (`AllowTcpForwarding`, which is on by default). Hardened nodes that turn it off cannot serve consoles.
- All bytes pass through the Go process. The overhead is negligible at VNC bandwidths.
- While a TCP listener is open, any local user on the Mac can connect to it, as with any `ssh -L` tunnel. This is acceptable on single-user workstations. The WebSocket entry point is protected by a token ([0006](0006-lock-down-the-local-web-server.md)).

## Alternatives considered

- **`ssh -N -L <port>:<target>:<vnc port>` per console.** This is the standard approach, but the app must pick a free port before ssh binds it, which is a race. It also runs one ssh process per tunnel, hides connection state, and cannot serve WebSocket clients.
- **websockify (noVNC's Python proxy) in front of `ssh -L`.** This adds a runtime dependency and a second hop for what is a short piece of Go.
- **Connecting to the node's VNC port directly.** The port is usually not routable, and this would send unauthenticated VNC across the network.
