# 0009. Let users choose the VNC viewer

- Status: Accepted
- Date: 2026-11-17
- Supersedes: the TigerVNC part of [0004](0004-open-consoles-in-the-browser-with-novnc.md), and the rule for closing the TCP relay in [0003](0003-relay-vnc-over-ssh-channels.md)

## Context

[0004](0004-open-consoles-in-the-browser-with-novnc.md) made TigerVNC the only native viewer, found through its macOS app bundle. Operators have their own preferences: Remmina on many Linux desktops, `remote-viewer` from virt-viewer, macOS Screen Sharing or RealVNC. With Linux support ([0008](0008-support-linux.md)), the app bundle lookup no longer covers everyone.

The relay already gives native viewers a plain loopback TCP port ([0003](0003-relay-vnc-over-ssh-channels.md)), so any VNC client can connect. Two facts shape how VNCMan should start one:

- Viewer commands may include the VM's name, for example as a window title. Tenants choose Nova instance names. If the command ran through a shell, a crafted VM name could run commands on the operator's machine.
- Some viewers hand the connection to an instance that is already running, and then exit at once. Remmina does this when it is already open, and so does `open` on macOS. So the viewer process exiting doesn't mean the console was closed.

## Decision

- **Add a `viewer` setting** to the config file ([0007](0007-keep-configuration-in-a-toml-file.md)). It is a list of arguments with placeholders, and VNCMan runs it directly, never through a shell:

  ```toml
  viewer = ["remmina", "-c", "vnc://{host}:{port}"]
  ```

  - `{host}` is the relay's address, which is always `127.0.0.1`.
  - `{port}` is the relay's port.
  - `{title}` is the VM's display name and node, for example `web01 · node1`.

  Placeholders are replaced within each argument, so a value never turns into extra arguments. An unknown placeholder is a config error.
- **Default to TigerVNC** with `["vncviewer", "{host}::{port}"]`. On macOS, if `vncviewer` isn't on the `PATH`, VNCMan uses the one inside TigerVNC.app, as it does today.
- **Document examples instead of building in presets.** The README lists commands for common viewers, for example:

  | Viewer | `viewer =` |
  |---|---|
  | TigerVNC | `["vncviewer", "{host}::{port}"]` |
  | virt-viewer | `["remote-viewer", "--title", "{title}", "vnc://{host}:{port}"]` |
  | Remmina | `["remmina", "-c", "vnc://{host}:{port}"]` |
  | macOS Screen Sharing | `["open", "vnc://{host}:{port}"]` |

  Each example is tested with its viewer before it goes into the README.
- **Rename the key and setting value.** `v` opens the configured viewer, replacing `t`. The `console` setting takes `"browser"` or `"viewer"`, replacing `"tigervnc"`. Nothing has been released, so `"tigervnc"` isn't kept as an alias.
- **Close the TCP relay on inactivity, not on viewer exit.** This replaces the rule in [0003](0003-relay-vnc-over-ssh-channels.md). The relay closes when VNCMan quits, when no viewer connects within 60 seconds of it opening, or 30 seconds after its last connection ends.
- **Reuse the relay when the user opens a VM again.** While the VM's relay is still open, VNCMan starts the viewer again on the same port. This lets the user reopen a viewer they just closed.
- **Report viewer errors.** If the viewer can't be started, or exits with an error within a few seconds, the status line shows its error output.

## Consequences

- Any VNC client works. Linux users can keep `remote-viewer`, the client they know from virt-viewer.
- A VM name can't inject commands, because nothing runs through a shell.
- An argument list is slightly less convenient to write than a single command string.
- A relay can stay open for up to 30 seconds after its viewer closes, or 60 seconds if no viewer ever connects. It listens only on loopback ([0003](0003-relay-vnc-over-ssh-channels.md)).
- VNCMan no longer knows when a viewer window closes. The open-console marker shows active connections instead.
- Third-party viewers behave differently. Some warn about unencrypted connections, and some ask for a password. That is outside VNCMan's control, so the README examples note what users should expect.

## Alternatives considered

- **A command string run through the shell**, such as `viewer = "remmina -c vnc://{host}:{port}"`. It is easier to write, but it brings quoting problems and lets VM names inject commands.
- **Built-in presets for each viewer.** Presets are easier to discover, but each one is code to maintain and test on two systems, while a documented example costs nothing. Presets can be added later if people ask for them.
- **Keep tracking the viewer process.** This is simple and precise for TigerVNC, but wrong for viewers that hand off to a running instance and exit.
- **Use the system's `vnc://` handler by default**, through `open` or `xdg-open vnc://…`. Which app handles `vnc://` varies from machine to machine, and there may be none. Users who want it can set it as their `viewer`.
