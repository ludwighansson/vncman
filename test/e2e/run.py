#!/usr/bin/env python3
"""End-to-end check of VNCMan against a fake node (ADR 0008).

Builds vncman, then runs it in a pseudo-terminal with fake ssh, virsh,
browser opener and VNC viewer on the PATH, and a fake VNC server. It drives
the terminal UI like a user, connects to the browser console's WebSocket
like noVNC would, and opens a viewer that hands off and exits like Remmina.

Run from anywhere: python3 test/e2e/run.py
To test a binary built another way, for example with go install, set
VNCMAN_BINARY to its path.
Needs: go, python3 and nc. Works on macOS and Linux.
"""
import base64
import fcntl
import http.client
import os
import pty
import re
import select
import shutil
import socket
import struct
import subprocess
import sys
import tempfile
import termios
import threading
import time
import urllib.parse

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))
ANSI = re.compile(rb"\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b[()][0-9A-Za-z]|\x1b[=>]")
GREETING = b"RFB 003.008\n"
# Static imports and re-exports, including those spanning several lines.
IMPORT = re.compile(r"""^\s*(?:(?:import|export)\s[\w\s{},*$]*?from\s*|import\s*)["']([^"']+)["']""", re.M)

screen = b""
failures = []


def check(cond, what):
    print(("PASS " if cond else "FAIL ") + what, flush=True)
    if not cond:
        if not failures:  # show what the terminal looked like at the first failure
            print(ANSI.sub(b"", screen[-2000:]).decode(errors="replace"))
        failures.append(what)


def fake_vnc_server(srv):
    """Sends the RFB greeting, then echoes whatever the client sends."""
    while True:
        conn, _ = srv.accept()

        def serve(conn):
            conn.sendall(GREETING)
            while data := conn.recv(4096):
                conn.sendall(data)
            conn.close()

        threading.Thread(target=serve, args=(conn,), daemon=True).start()


def pump(fd, seconds):
    global screen
    end = time.time() + seconds
    while time.time() < end:
        r, _, _ = select.select([fd], [], [], 0.05)
        if r:
            try:
                screen += os.read(fd, 65536)
            except OSError:
                return


def wait_for(fd, text, seconds=10):
    end = time.time() + seconds
    while time.time() < end:
        pump(fd, 0.1)
        if text.encode() in ANSI.sub(b"", screen[-30000:]):
            return True
    return False


def wait_for_file(fd, path, seconds=10):
    end = time.time() + seconds
    while time.time() < end:
        pump(fd, 0.1)
        if os.path.exists(path) and os.path.getsize(path) > 0:
            with open(path, "rb") as f:
                return f.read()
    return b""


def keys(fd, *presses):
    for k in presses:
        os.write(fd, k)
        time.sleep(0.08)


def ws_connect(host, port, path, origin):
    s = socket.create_connection((host, port), timeout=5)
    key = base64.b64encode(os.urandom(16)).decode()
    s.sendall((f"GET {path} HTTP/1.1\r\nHost: {host}:{port}\r\nUpgrade: websocket\r\n"
               f"Connection: Upgrade\r\nSec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n"
               f"Origin: {origin}\r\n\r\n").encode())
    resp = b""
    while b"\r\n\r\n" not in resp:
        chunk = s.recv(1)
        if not chunk:
            break
        resp += chunk
    return s, resp.split(b"\r\n")[0].decode()


def ws_read(s):
    hdr = s.recv(2)
    if len(hdr) < 2:
        return b""
    length = hdr[1] & 0x7F
    if length == 126:
        length = struct.unpack(">H", s.recv(2))[0]
    data = b""
    while len(data) < length:
        data += s.recv(length - len(data))
    return data


def ws_send(s, payload):
    mask = os.urandom(4)
    masked = bytes(b ^ mask[i % 4] for i, b in enumerate(payload))
    s.sendall(bytes([0x82, 0x80 | len(payload)]) + mask + masked)


def main():
    # A short temporary home keeps ssh's control socket path under the
    # macOS limit and keeps the run away from the real ~/Library.
    tmp = tempfile.mkdtemp(prefix="vm-", dir="/tmp")
    try:
        run(tmp)
    finally:
        shutil.rmtree(tmp, ignore_errors=True)
    if failures:
        print(f"\n{len(failures)} check(s) failed")
        sys.exit(1)
    print("\nall checks passed")


def load_modules(conn, page):
    """Follows the console page's module imports like a browser would.

    Returns the paths that failed to load and the paths that loaded."""
    base = "/novnc/"  # the page's <base href>
    queue = [urllib.parse.urljoin(base, spec) for spec in IMPORT.findall(page)]
    failed, loaded = {}, set()
    while queue:
        path = queue.pop()
        if path in loaded or path in failed:
            continue
        conn.request("GET", path)
        r = conn.getresponse()
        body = r.read().decode(errors="replace")
        if r.status != 200:
            failed[path] = r.status
            continue
        loaded.add(path)
        queue.extend(urllib.parse.urljoin(path, spec) for spec in IMPORT.findall(body))
    return [f"{path} ({status})" for path, status in sorted(failed.items())], loaded


def run(tmp):
    binary = os.environ.get("VNCMAN_BINARY")
    if binary:
        binary = os.path.abspath(binary)
    else:
        binary = os.path.join(tmp, "vncman")
        subprocess.run(["go", "build", "-o", binary, "./cmd/vncman"], cwd=REPO, check=True)

    # The temporary directory is the home directory itself: one level deeper
    # and macOS's control socket path limit would be exceeded.
    home = tmp
    state = os.path.join(tmp, "e2e-state")
    os.makedirs(state)
    config = os.path.join(tmp, "config.toml")
    with open(config, "w") as f:
        f.write('viewer = ["fake-viewer", "{host}", "{port}", "{title}"]\n')

    srv = socket.socket()
    srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    srv.bind(("127.0.0.1", 0))
    srv.listen()
    vnc_port = srv.getsockname()[1]
    threading.Thread(target=fake_vnc_server, args=(srv,), daemon=True).start()

    env = dict(
        os.environ,
        PATH=os.path.join(HERE, "bin") + ":" + os.environ["PATH"],
        HOME=home,
        XDG_CONFIG_HOME=os.path.join(home, ".config"),
        XDG_STATE_HOME=os.path.join(home, ".local", "state"),
        XDG_CACHE_HOME=os.path.join(home, ".cache"),
        E2E_DIR=HERE,
        E2E_STATE=state,
        E2E_VNC_PORT=str(vnc_port),
        TERM="xterm-256color",
    )
    pid, fd = pty.fork()
    if pid == 0:
        os.execve(binary, ["vncman", "--config", config, "node1"], env)
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 140, 0, 0))

    def ssh_log():
        try:
            return open(os.path.join(state, "ssh.log")).read()
        except FileNotFoundError:
            return ""

    # Listing.
    check(wait_for(fd, "web01"), "VM list shows web01")
    check(wait_for(fd, "web-prod-1") and wait_for(fd, "acme-prod"), "Nova instance shows its name and project")
    check(wait_for(fd, "2 running"), "header shows 2 running")
    if failures:
        os.kill(pid, 9)
        return
    check("node1 true" in ssh_log(), "connected the master before the UI started")
    check("ControlMaster=no -o BatchMode=yes -T node1 virsh -r -c qemu:///system list --uuid --state-running" in ssh_log(),
          "listed with read-only virsh in batch mode, without sudo")

    # Browser console.
    keys(fd, b"/", b"w", b"e", b"b", b"0", b"1")
    check(wait_for(fd, "1 shown"), "filter narrows the list")
    keys(fd, b"\r", b"\r")  # leave the filter, then open the console
    url = wait_for_file(fd, os.path.join(state, "open.log")).decode().strip()
    check(url.startswith("http://127.0.0.1:") and "/console/" in url, f"Enter opened a console URL ({url})")
    check(wait_for(fd, "Opened web01 in your browser"), "status confirms the browser console")

    u = urllib.parse.urlparse(url)
    token = u.path.rsplit("/", 1)[-1]
    conn = http.client.HTTPConnection(u.hostname, u.port, timeout=5)
    conn.request("GET", u.path + "?" + u.query)
    resp = conn.getresponse()
    page = resp.read().decode()
    check(resp.status == 200 and '<base href="/novnc/">' in page and "web01 · node1" in page,
          "console page is noVNC with the VM's title")
    missing, loaded = load_modules(conn, page)
    check(not missing and "/novnc/vendor/pako/lib/zlib/inflate.js" in loaded,
          f"all {len(loaded)} modules the console page imports are served" +
          (f"; missing: {', '.join(missing)}" if missing else ""))

    s, status = ws_connect(u.hostname, u.port, f"/ws/{token}", "https://attacker.example")
    s.close()
    check(" 403 " in status, f"WebSocket from another origin is refused ({status})")
    s, status = ws_connect(u.hostname, u.port, f"/ws/{token}", f"http://127.0.0.1:{u.port}")
    check(" 101 " in status, f"WebSocket from the console page is accepted ({status})")
    check(ws_read(s) == GREETING, "VNC server greeting arrives in the browser through ssh -W")
    ws_send(s, b"hello")
    check(ws_read(s) == b"hello", "bytes from the browser reach the VNC server and back")
    check(wait_for(fd, "●"), "open console is marked in the list")
    check(f"-W 127.0.0.1:{vnc_port} node1" in ssh_log(), "relay used ssh -W to the VM's VNC port")
    check("dumpxml 610964c9-5469-4c62-8737-233c94242fae'" in ssh_log(), "port was looked up again before connecting")

    keys(fd, b"q")
    check(wait_for(fd, "1 console is open. Quit and close them?"), "quitting with an open console asks first")
    keys(fd, b"n")
    s.close()
    time.sleep(1)

    # Viewer that hands off and exits at once, like Remmina.
    keys(fd, b"v")
    greeting = wait_for_file(fd, os.path.join(state, "viewer-greeting.log"))
    check(greeting == GREETING, "a viewer that exits at once still reaches the VM through the relay")
    args = open(os.path.join(state, "viewer-args.log")).read().splitlines()
    check(len(args) == 3 and args[0] == "127.0.0.1" and args[1].isdigit() and args[2] == "web01 · node1",
          f"viewer got {{host}}, {{port}} and {{title}} as separate arguments ({args})")
    check(wait_for(fd, "Opened web01 in fake-viewer"), "status names the viewer")
    # The fake viewer disconnects after 3 seconds; the relay then gives ssh up
    # to 2 seconds to exit.
    time.sleep(6)

    # Quit.
    keys(fd, b"q")
    status = None
    end = time.time() + 10
    while time.time() < end:
        pump(fd, 0.1)
        done, st = os.waitpid(pid, os.WNOHANG)
        if done:
            status = st
            break
    if status is None:
        os.kill(pid, 9)
        print(ANSI.sub(b"", screen[-3000:]).decode(errors="replace"))
    check(status is not None and os.waitstatus_to_exitcode(status) == 0, "vncman exits cleanly")
    check("-O exit node1" in ssh_log(), "the SSH master was closed on quit")


if __name__ == "__main__":
    main()
