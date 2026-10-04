#!/usr/bin/env python3
"""Native menu client contract checks; no host VPN/DNS mutations."""
import copy
import datetime
import json
from pathlib import Path
import socket
import subprocess
import tempfile
import threading

ROOT = Path(__file__).resolve().parent.parent
APP = ROOT / 'bin/Meldnet.app/Contents/MacOS/Meldnet'
now = datetime.datetime.now(datetime.timezone.utc)
stamp = now.isoformat()
old = (now - datetime.timedelta(minutes=5)).isoformat()
status = {'backend': 'wireguard', 'node': {'public_key': 'self', 'settings': {'name': 'laptop'}},
          'tunnel': {'up': True, 'peers': [{'public_key': 'primary', 'last_handshake': stamp}]}}
network = {'role': 'client', 'members': [
    {'name': name, 'public_key': key, 'hostname': hostname, 'last_seen': seen}
    for name, key, hostname, seen in [
        ('primary', 'primary', 'primary.meldnet.internal', stamp),
        ('laptop', 'self', 'laptop.meldnet.internal', stamp),
        ('active', 'active', 'active.meldnet.internal', stamp),
        ('idle', 'idle', 'idle.meldnet.internal', old),
        ('legacy', 'legacy', None, '0001-01-01T00:00:00Z')]]}


def run(s, n, bad=False):
    failures = []
    with tempfile.TemporaryDirectory(prefix='meldnet-menu-') as temp:
        path = str(Path(temp) / 'api.sock')
        server = socket.socket(socket.AF_UNIX)
        server.bind(path)
        server.listen()
        server.settimeout(10)

        def serve():
            try:
                for route, payload in [('/v1/status', s), ('/v1/network', n)]:
                    conn, _ = server.accept()
                    with conn:
                        conn.settimeout(5)
                        request = b''
                        while b'\r\n\r\n' not in request:
                            request += conn.recv(4096)
                        assert request.startswith(f'GET {route} HTTP/1.0\r\n'.encode())
                        assert b'Host: meldnet\r\n' in request
                        body = json.dumps(payload).encode()
                        code = '403 Forbidden' if bad else '200 OK'
                        response = f'HTTP/1.0 {code}\r\nContent-Length: {len(body)}\r\n\r\n'.encode() + body
                        for offset in range(0, len(response), 17):
                            conn.sendall(response[offset:offset+17])
                    if bad:
                        break
            except Exception as error:
                failures.append(error)
            finally:
                server.close()

        thread = threading.Thread(target=serve)
        thread.start()
        result = subprocess.run([str(APP), '--socket', path, '--check'], capture_output=True, text=True, timeout=15)
        thread.join(timeout=12)
        assert not thread.is_alive()
        assert not failures, failures
        if bad:
            assert result.returncode != 0
            return
        assert result.returncode == 0, result.stderr
        return {row['name']: row for row in json.loads(result.stdout)}

rows = run(status, network)
assert rows['primary']['status'] == 'Recent handshake'
assert rows['laptop']['status'] == 'This Mac'
assert rows['active']['status'] == 'Active · control'
assert rows['idle']['status'] == 'Inactive · control'
assert rows['legacy'].get('hostname') is None
assert rows['active']['hostname'] == 'active.meldnet.internal'
stale = copy.deepcopy(network)
stale['error'] = 'Cannot synchronize'
assert run(status, stale)['active']['status'] == 'Status unknown'
down = copy.deepcopy(status)
down['tunnel']['up'] = False
assert run(down, network)['primary']['status'] == 'VPN disconnected'
simulation = copy.deepcopy(status)
simulation['backend'] = 'simulation'
assert all(row['status'] == 'Simulation' for row in run(simulation, network).values())
assert run(status, {'role': 'manual', 'members': None}) == {}
run(status, network, bad=True)
result = subprocess.run([str(APP), '--socket', '/nonexistent/meldnet.sock', '--check'], capture_output=True, timeout=10)
assert result.returncode != 0
print('PASS: fragmented HTTP, exact API/Host, peer status, stale directory, disconnect, simulation, missing hostname, empty directory, denied/unavailable daemon')

# Compile the same API implementation with a test-only stdin driver. Production
# app arguments never accept enrollment credentials or diagnostic mutations.
with tempfile.TemporaryDirectory(prefix='meldnet-mutations-') as temp:
    driver = str(Path(temp) / 'client')
    subprocess.run(['swiftc', '-swift-version', '5', str(ROOT / 'macos/Meldnet/API.swift'),
                    str(ROOT / 'macos/Tests/main.swift'), '-o', driver], check=True)
    for action, code, reply in [('up', 200, {'ok': True}), ('down', 200, {'ok': True}),
                                ('join', 200, {'ok': True}), ('join', 400, {'error': 'test-key-private'}),
                                ('profile-up', 200, {'ok': True}), ('profile-down', 200, {'ok': True}),
                                ('profile-auto', 200, {'ok': True}), ('profile-join', 409, {'error': 'overlap'}),
                                ('profiles', 200, [{'id': 'work', 'auto_connect': False, 'status': status, 'network': network}]),
                                ('up', 200, {'ok': False}), ('down', 500, {'error': 'cleanup failed'})]:
        path = str(Path(temp) / 'api.sock')
        server = socket.socket(socket.AF_UNIX)
        server.bind(path)
        server.listen()
        server.settimeout(10)
        errors = []
        payload = {'action': action, 'name': 'test-device', 'key': 'test-key-private', 'id': 'work'}

        def serve_mutation():
            try:
                conn, _ = server.accept()
                with conn:
                    conn.settimeout(5)
                    request = b''
                    while b'\r\n\r\n' not in request:
                        part = conn.recv(4096)
                        assert part
                        request += part
                    head, body = request.split(b'\r\n\r\n', 1)
                    route = '/v1/network/join' if action == 'join' else '/v1/' + action
                    method = 'POST'
                    if action.startswith('profile-'):
                        operation = action.removeprefix('profile-')
                        route = '/v1/networks/work/' + operation
                        if operation == 'auto':
                            method, route = 'PUT', '/v1/networks/work/autoconnect'
                        elif operation == 'join':
                            route = '/v1/networks/join'
                    elif action == 'profiles':
                        method, route = 'GET', '/v1/networks'
                    assert head.startswith(f'{method} {route} HTTP/1.0\r\n'.encode())
                    assert b'Host: meldnet' in head
                    assert b'Content-Type: application/json' in head
                    length = int(next(line.split(b':', 1)[1] for line in head.split(b'\r\n') if line.startswith(b'Content-Length:')))
                    while len(body) < length:
                        part = conn.recv(4096)
                        assert part
                        body += part
                    if action == 'profile-join':
                        assert json.loads(body) == {'id': 'work', 'name': 'test-device', 'key': 'test-key-private', 'auto_connect': False, 'connect': True}
                    elif action == 'profile-auto':
                        assert json.loads(body) == {'auto_connect': False}
                    elif action == 'join':
                        assert json.loads(body) == {'name': 'test-device', 'key': 'test-key-private'}
                    else:
                        assert body == b''
                    data = json.dumps(reply).encode()
                    conn.sendall(f'HTTP/1.0 {code} Response\r\nContent-Length: {len(data)}\r\n\r\n'.encode() + data)
            except Exception as error:
                errors.append(error)
            finally:
                server.close()

        thread = threading.Thread(target=serve_mutation)
        thread.start()
        result = subprocess.run([driver, path], input=json.dumps(payload), capture_output=True, text=True, timeout=15)
        thread.join(timeout=12)
        assert not thread.is_alive()
        assert not errors, errors
        success = code == 200 and (action == 'profiles' or reply.get('ok') is True)
        assert (result.returncode == 0) == success, result.stderr
        assert 'test-key-private' not in result.stdout + result.stderr
        Path(path).unlink()
print('PASS: legacy and scoped profile APIs, independent up/down/startup controls, list decoding, overlap rejection, framing, typed JSON, enrollment-key redaction')

# Execute only a fake TUI in a temporary directory, not Terminal or the real VPN.
with tempfile.TemporaryDirectory(prefix='meldnet-launcher-test-') as temp:
    directory = Path(temp)
    driver = directory / 'launcher-test'
    subprocess.run(['swiftc', str(ROOT / 'macos/Meldnet/TUILauncher.swift'),
                    str(ROOT / 'macos/Tests/Launcher/main.swift'), '-o', str(driver)], check=True)
    fake = directory / "client's $(touch INJECTED) `touch INJECTED`"
    fake.write_text('#!/usr/bin/python3\nimport json, sys\nprint(json.dumps(sys.argv[1:]))\n')
    fake.chmod(0o700)
    custom_socket = "socket's $(touch INJECTED) `touch INJECTED`\nwith a newline.sock"
    result = subprocess.run([str(driver)], input=json.dumps({'executable': str(fake), 'socket': custom_socket}),
                            capture_output=True, text=True, check=True)
    launcher = Path(result.stdout.strip())
    assert launcher.stat().st_mode & 0o777 == 0o700
    assert launcher.parent.stat().st_mode & 0o777 == 0o700
    try:
        launched = subprocess.run(['/bin/sh', str(launcher)], cwd=directory, capture_output=True, text=True, check=True)
        assert json.loads(launched.stdout) == ['--socket', custom_socket, 'tui']
        assert not (directory / 'INJECTED').exists()
        assert not launcher.parent.exists()
    finally:
        if launcher.exists():
            launcher.unlink()
            launcher.parent.rmdir()
print('PASS: TUI launcher keeps exact socket/arguments, handles shell metacharacters, uses private permissions and removes its temporary files')
