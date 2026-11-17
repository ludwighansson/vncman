# 0002. Use the system OpenSSH client

- Status: Accepted
- Date: 2026-11-16

## Context

VNCMan reaches nodes over SSH, both to list VMs ([0001](0001-discover-vms-with-read-only-virsh-over-ssh.md)) and to carry VNC traffic ([0003](0003-relay-vnc-over-ssh-channels.md)). Operators already have SSH to their nodes working in a terminal, and that setup is often far from trivial:

- `~/.ssh/config` with host aliases, users, `ProxyJump` bastions, and `Match` and `Include` blocks
- keys held in ssh-agent or the macOS keychain, FIDO2 security keys (`sk-ed25519`), and certificates
- `known_hosts` entries that already pin the nodes' host keys

VNCMan runs in the user's terminal ([0005](0005-browse-in-a-terminal-ui-built-with-go.md)), so it inherits `PATH`, `SSH_AUTH_SOCK` and the rest of the shell environment. `ssh` resolves to the same binary the user runs by hand, for example Homebrew's OpenSSH ahead of `/usr/bin/ssh`.

However, the terminal UI owns the terminal most of the time. If ssh asked for a passphrase, a host-key confirmation or a security-key touch while the UI holds the terminal in raw mode, the prompt would garble the screen and steal keystrokes. Some ssh calls are also triggered from the browser while the UI is running, when a noVNC console connects.

## Decision

We will run `ssh` from `PATH` for all SSH work and never implement SSH in-process.

- **One connection per node.** Every invocation passes `-o ControlPath=<cache>/cm/%C`, where `<cache>` is `~/Library/Caches/vncman`. The listing and every console reuse the node's master, so the user authenticates, or touches their key, once per node. `%C` keeps the socket path under the 104-byte UNIX socket path limit on macOS. VNCMan uses its own control path, so it never interferes with masters the user runs.
- **Authenticate up front, in the terminal.** When the user opens a node, VNCMan checks for a live master with `ssh -O check`. If there is none, it connects before the UI starts (for a node given on the command line), or suspends the UI (Bubble Tea's `tea.ExecProcess` hands the terminal back) and runs `ssh -o ControlMaster=auto -o ControlPersist=<ssh.control_persist> <node> true` in the foreground. Passphrase, host-key and security-key prompts appear in the terminal as usual. The session ends at once, ssh moves the master to the background, and the UI resumes. `ssh.control_persist` defaults to `10m`.
- **Never prompt afterwards.** All other ssh calls, including the listing and console relays, check for the master first. They pass `-o ControlMaster=no -o BatchMode=yes`, so they reuse the master and never prompt. If the master has gone away, they fail at once. Without that check, a fresh connection could wait silently for a security-key touch that BatchMode does not prevent. The UI then shows the error and offers to reconnect, which repeats the step above.
- **Keep host key checking as the user configured it.** VNCMan never passes `StrictHostKeyChecking=no` or `UserKnownHostsFile=/dev/null`.
- **Show errors verbatim.** When ssh fails, the UI shows its stderr.
- **Clean up on quit.** VNCMan runs `ssh -O exit` for each master it opened.

## Consequences

- Everything that works in the terminal works in VNCMan, including bastions, agents, security keys and certificates. There is no SSH or crypto code to maintain.
- Node names in VNCMan are plain SSH destinations, so `~/.ssh/config` aliases just work.
- No askpass dialogs or environment probing are needed, because VNCMan runs where the user's ssh already works.
- VNCMan must be started from a terminal. The later app-window version ([0005](0005-browse-in-a-terminal-ui-built-with-go.md)) starts without one, so it will need askpass prompts and environment resolution. That is a separate decision.
- If a master dies while consoles are in use, new connections fail until the user reconnects from the UI.
- If VNCMan crashes, its masters stay open until `ControlPersist` expires. They grant no more access than the user already has.

## Alternatives considered

- **`golang.org/x/crypto/ssh`.** It is self-contained, but VNCMan would have to reimplement `ssh_config` parsing (`ProxyJump`, `Match`, `Include`), agent selection, FIDO keys, certificates and `known_hosts` handling. It would still drift from what the terminal does.
- **Let each ssh call prompt when it needs to.** This is simpler, but prompts would collide with the UI, and calls started from the browser have nowhere to prompt.
- **`SSH_ASKPASS` with native dialogs.** This works without a terminal, but it isn't needed while VNCMan runs in one. It is deferred to the app-window version.
