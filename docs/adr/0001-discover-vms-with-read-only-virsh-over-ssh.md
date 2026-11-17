# 0001. Discover VMs with read-only `virsh` over SSH

- Status: Accepted
- Date: 2026-11-16

## Context

A node is a libvirt/KVM hypervisor, typically an OpenStack Nova compute node. To show a VM list, VNCMan needs the following for each running domain:

- the libvirt name, UUID and state
- the VNC graphics settings: listen address and port
- for Nova instances, the instance display name, project and flavor. A libvirt name such as `instance-0001a2b3` means nothing to a person, and support requests usually quote the instance name or UUID.

On Linux, `virt-viewer --connect qemu+ssh://<node>/system` gets this through libvirt's remote protocol, which needs the libvirt client library on the workstation. Operators already have SSH access to nodes, and `virsh` is present wherever libvirt is.

The SSH user on the nodes is not in the `libvirt` group. On the node tested (node1), read-only `virsh -r` still works without sudo, because libvirt's read-only socket is open to all users. That is a deployment setting, so other nodes may lock the socket down and require sudo. Where sudo is needed, it may ask for a password, depending on the node's sudoers rules. VNCMan runs remote commands without a terminal ([0002](0002-use-the-system-openssh-client.md)), so sudo cannot prompt for one there.

Some nodes run plain libvirt VMs with readable names, such as `web01`, rather than Nova instances. Those domains have no Nova metadata.

## Decision

We will list VMs by running read-only `virsh` on the node over SSH ([0002](0002-use-the-system-openssh-client.md)), using `sudo` only when it is needed, and parse the domain XML locally in Go.

- **Each refresh runs two commands** over the node's SSH master:

  ```sh
  [sudo <flags>] virsh -r -c qemu:///system list --uuid --state-running
  [sudo <flags>] virsh -r -c qemu:///system 'dumpxml <uuid-1>; dumpxml <uuid-2>; …'
  ```

  virsh accepts several `;`-separated commands in a single argument. Each step is therefore a single invocation of `virsh` and nothing else. Under sudo, that keeps the sudoers rule narrow, and a password is needed only once per command. The commands use UUIDs because domain names may contain characters the shell would split on.
- **The `sudo` setting chooses how VNCMan gets access** ([0007](0007-keep-configuration-in-a-toml-file.md)). It can be `"auto"` (the default), `"always"` or `"never"`. In `"auto"` mode:
  1. VNCMan first runs `virsh` without sudo, which is the least privilege that can work.
  2. If that fails, VNCMan retries with `sudo -n`, which makes sudo fail instead of prompting. On nodes with a `NOPASSWD` rule, nothing more is needed.
  3. If sudo reports that a password is required, the terminal UI shows a masked password field for that node ([0005](0005-browse-in-a-terminal-ui-built-with-go.md)). VNCMan then runs `sudo -S -p ''` and writes the password to sudo's standard input.
  4. VNCMan remembers which step worked for each node for the rest of the run. If every step fails, it shows the error from each one.

  `"always"` starts at step 2, and `"never"` stops after step 1.
- **The sudo password is handled carefully.** VNCMan keeps it in memory for that node until it quits, so the user enters it once per node per run. It never writes the password to disk, and never puts it in a command line or an environment variable. If sudo rejects the password, VNCMan discards it and asks again.
- **The `virsh` command can be changed per node.** It defaults to `virsh -r -c qemu:///system`. Containerized libvirt, such as Kolla, can use `virsh = "docker exec nova_libvirt virsh -r"`.
- **The libvirt connection is always read-only.** VNCMan always passes `-r`, so it cannot change VM state, including when sudo runs it as root.
- **Consoles need no sudo.** The relay connects to the VNC server's TCP port, which any user on the node can reach ([0003](0003-relay-vnc-over-ssh-channels.md)). Only the listing may need privileges.
- VNCMan parses these elements locally:
  - `<name>` and `<uuid>`
  - `<graphics type="vnc">`: the `port`, and the `listen` attribute or `<listen type="address">` child
  - `<metadata><nova:instance>`: `nova:name`, `nova:owner/nova:project` and the `nova:flavor` name
- VNCMan lists only running domains, because a shut-off domain has no VNC port.
- Some domains are listed but marked not connectable: those that are SPICE-only or headless, and those whose VNC server is bound to a UNIX socket.
- Nova metadata is optional. When it is missing or cannot be parsed, VNCMan shows the libvirt name.

## Consequences

- Nothing needs to be installed on the node, and the Mac only needs VNCMan itself.
- The same commands can be run by hand on a node to see what VNCMan sees.
- On nodes where libvirt's read-only socket is open, as on node1, VNCMan needs no sudo or configuration at all.
- On nodes that need sudo, admins can avoid the password prompt with a `NOPASSWD` rule limited to read-only virsh, for example `<user> ALL=(root) NOPASSWD: /usr/bin/virsh -r *`. Without such a rule, VNCMan asks for the password once per node per run.
- In `"auto"` mode, a node that needs sudo costs one failed command on its first refresh in each run.
- The sudo password stays in VNCMan's memory while it runs.
- v1 does not support nodes whose sudoers sets `requiretty`, because VNCMan runs commands without a terminal.
- A refresh is two SSH commands on an existing connection, and a single virsh process dumps every domain.
- VNCMan depends on libvirt's domain XML, which is a stable public format. It also depends on Nova's metadata block, which is not a formal API, so the parser must tolerate missing fields.
- Read-only `dumpxml` leaves out VNC passwords. VNC servers that require authentication are out of scope for v1 ([0004](0004-open-consoles-in-the-browser-with-novnc.md)).

## Alternatives considered

- **Run `sudo -v` once in the terminal and rely on cached credentials.** By default sudo caches credentials per terminal session, so later commands over new SSH sessions would prompt again.
- **Use one `sudo sh -c '…'` loop per refresh.** This needs only one privileged command, but it requires sudo rights to a shell instead of just `virsh`.
- **Read `/run/libvirt/qemu/*.xml` with `sudo cat`.** This is also a single command, but those files hold libvirt's internal state and are not a stable interface.
- **Use the libvirt remote protocol from Go**, for example `digitalocean/go-libvirt` over an SSH-forwarded socket. This gives a typed API, but it needs socket access, which is the same privilege problem, and it is harder to adapt to containerized libvirt.
- **Use a local libvirt client** (`brew install libvirt` with a `qemu+ssh://` URI). This adds a dependency on every Mac and has no way to run under sudo on the node.
- **Use the OpenStack API** to list the servers on a host and fetch their console URLs. This needs admin credentials and does not cover VMs outside Nova.
