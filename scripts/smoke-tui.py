#!/usr/bin/env python3
"""Exercise the actual TUI in a PTY against a temporary simulated daemon."""
import base64
import fcntl
import json
import os
from pathlib import Path
import pty
import select
import signal
import struct
import subprocess
import tempfile
import termios
import time

root = Path(__file__).resolve().parent.parent


def main():
    with tempfile.TemporaryDirectory(prefix="mn-", dir="/tmp") as directory:
        socket = str(Path(directory) / "control.sock")
        command = [str(root / "bin/meldnet"), "--socket", socket]
        daemon = subprocess.Popen(
            [str(root / "bin/meldnetd"), "--simulate", "--data-dir",
             str(Path(directory) / "state"), "--socket", socket],
            stdout=subprocess.DEVNULL, stderr=subprocess.PIPE,
        )
        app = None
        master = slave = None
        try:
            for _ in range(100):
                if Path(socket).exists():
                    break
                if daemon.poll() is not None:
                    raise RuntimeError("simulation daemon exited")
                time.sleep(0.05)
            subprocess.run(command + ["init", "--name", "smoke", "--addresses", "10.77.0.1/24"], check=True, stdout=subprocess.DEVNULL)
            settings = json.loads(subprocess.check_output(command + ["config"]))
            # Synthetic public key only; no real credentials enter this fixture.
            settings["settings"]["peers"] = [{
                "name": "test-peer", "public_key": base64.b64encode(bytes(range(1, 33))).decode(),
                "endpoint": "127.0.0.1:51821", "allowed_ips": ["10.77.0.2/32"], "keepalive": 25,
            }]
            subprocess.run(command + ["apply", "-"], input=json.dumps(settings).encode(), check=True, stdout=subprocess.DEVNULL)
            master, slave = pty.openpty()
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
            env = dict(os.environ, TERM="xterm-256color")
            app = subprocess.Popen(command, stdin=slave, stdout=slave, stderr=slave, env=env, start_new_session=True)
            capture = bytearray()

            def pump(seconds):
                deadline = time.monotonic() + seconds
                while time.monotonic() < deadline:
                    if select.select([master], [], [], 0.05)[0]:
                        data = os.read(master, 65536)
                        capture.extend(data)
                        # Answer common terminal capability probes.
                        if b"\x1b[c" in data:
                            os.write(master, b"\x1b[?1;2c")
                        if b"\x1b[6n" in data:
                            os.write(master, b"\x1b[1;1R")

            pump(2)
            os.write(master, b"c")
            pump(2)
            os.write(master, b"q")
            pump(0.5)
            app.wait(timeout=5)
            assert app.returncode == 0, "TUI failed"
            assert b"MELDNET" in capture and b"SIMULATION" in capture, "TUI did not render its identity/mode"
            status = json.loads(subprocess.check_output(command + ["status"]))
            assert status["tunnel"]["up"], "closing the TUI disconnected the daemon"
            assert all("last_handshake" not in p for p in status["tunnel"]["peers"]), "simulation invented a handshake"
            subprocess.run(command + ["down"], check=True)
            print("PASS: native Bubble Tea PTY render, connect, quit independence, and simulation labeling")
        finally:
            if app is not None and app.poll() is None:
                app.kill()
                app.wait()
            for fd in (master, slave):
                if fd is not None:
                    os.close(fd)
            daemon.send_signal(signal.SIGTERM)
            _, diagnostic = daemon.communicate(timeout=40)
            if daemon.returncode:
                raise RuntimeError(diagnostic.decode())


if __name__ == "__main__":
    main()
