#!/usr/bin/env python3
"""Real Bubble Tea enrollment against two unprivileged simulated daemons."""
import fcntl
import json
import os
from pathlib import Path
import pty
import select
import socket
import struct
import subprocess
import tempfile
import termios
import time

ROOT = Path(__file__).resolve().parent.parent


def main():
    daemons, fds = [], []
    app = None
    with tempfile.TemporaryDirectory(prefix="mn-join-", dir="/tmp") as directory:
        try:
            with socket.socket() as listener:
                listener.bind(("127.0.0.1", 0))
                port = listener.getsockname()[1]
            clients = []
            for name in ("primary", "client"):
                base = Path(directory) / name
                command = [str(ROOT / "bin/meldnet"), "--socket", str(base / "control.sock")]
                args = [str(ROOT / "bin/meldnetd"), "--simulate", "--data-dir", str(base / "state"), "--socket", str(base / "control.sock")]
                if name == "primary":
                    args += ["--primary", "--listen", f"127.0.0.1:{port}", "--public-url", f"https://127.0.0.1:{port}", "--endpoint", "127.0.0.1:51820"]
                daemons.append(subprocess.Popen(args, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE))
                for _ in range(100):
                    if (base / "control.sock").exists():
                        break
                    time.sleep(0.05)
                clients.append(command)
            enrollment = subprocess.check_output(clients[0] + ["invite"]).strip()
            master, slave = pty.openpty()
            fds = [master, slave]
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
            app = subprocess.Popen(clients[1], stdin=slave, stdout=slave, stderr=slave, env=dict(os.environ, TERM="xterm-256color"), start_new_session=True)
            capture = bytearray()

            def pump(seconds):
                deadline = time.monotonic() + seconds
                while time.monotonic() < deadline:
                    if select.select([master], [], [], 0.05)[0]:
                        data = os.read(master, 65536)
                        capture.extend(data)
                        if b"\x1b[c" in data:
                            os.write(master, b"\x1b[?1;2c")
                        if b"\x1b[6n" in data:
                            os.write(master, b"\x1b[1;1R")

            pump(2)
            os.write(master, b"j")
            pump(0.3)
            os.write(master, b"laptop\t")
            pump(0.3)
            os.write(master, b"\x1b[200~" + enrollment + b"\x1b[201~")
            pump(0.5)
            assert enrollment not in capture, "enrollment key leaked in TUI"
            os.write(master, b"\x13")  # Ctrl+S
            pump(4)
            status = json.loads(subprocess.check_output(clients[1] + ["status"]))
            assert status["tunnel"]["up"], "automatic connection failed"
            assert status["node"]["settings"]["addresses"] == ["10.77.0.2/32"]
            assert b"DEVICES" in capture and b"10.77.0.1" in capture, "directory not rendered"
            os.write(master, b"q")
            pump(0.5)
            app.wait(timeout=5)
            assert app.returncode == 0
            assert json.loads(subprocess.check_output(clients[1] + ["status"]))["tunnel"]["up"]
            print("PASS: TUI masked enrollment, automatic configuration, device directory, and quit independence")
        finally:
            if app is not None and app.poll() is None:
                app.kill()
                app.wait()
            for fd in fds:
                os.close(fd)
            for daemon in daemons:
                daemon.terminate()
            for daemon in daemons:
                _, errors = daemon.communicate(timeout=40)
                if daemon.returncode:
                    raise RuntimeError(errors.decode())


if __name__ == "__main__":
    main()
