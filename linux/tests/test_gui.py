import importlib.util
import json
import threading
import socket
import tempfile
import unittest
from pathlib import Path
from unittest import mock

spec = importlib.util.spec_from_file_location('meldnet_gui', Path(__file__).parents[1] / 'meldnet_gui.py')
gui = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gui)


def profile():
    return {'id': 'work', 'auto_connect': False, 'status': {'backend': 'wireguard-go', 'tunnel': {'up': True, 'peers': []}}, 'network': {'members': [{'name': 'laptop', 'hostname': 'laptop.work.meldnet.internal', 'public_key': 'public', 'last_seen': '2026-01-01T00:00:00Z'}]}}


class GUIModelTests(unittest.TestCase):
    def test_status_never_claims_reachability(self):
        p = profile()
        now = 1767225600
        self.assertEqual(gui.state_label(p), 'Interface up')
        self.assertEqual(gui.peer_rows(p, now)[0][1], 'Active · control')
        p['status']['tunnel']['peers'] = [{'public_key': 'public', 'last_handshake': '2026-01-01T00:00:00Z'}]
        self.assertEqual(gui.peer_rows(p, now)[0][1], 'Recent handshake')
        p['status']['backend'] = 'simulation'
        self.assertEqual(gui.peer_rows(p, now)[0][1], 'Simulation')
        self.assertEqual(gui.state_label(p), 'Simulation')

    def test_stale_and_disconnected(self):
        p = profile()
        p['network']['error'] = 'sync failed'
        self.assertEqual(gui.peer_rows(p, 1767225600)[0][1], 'Directory stale')
        p['status']['tunnel']['up'] = False
        self.assertEqual(gui.peer_rows(p)[0][1], 'VPN disconnected')
        self.assertFalse(gui.recent('invalid', 15, 0))
        self.assertFalse(gui.recent('2027-01-01T00:00:00Z', 15, 0))

    def test_actions_scope_profiles_and_separate_startup_policy(self):
        api = gui.LocalAPI('/unused')
        with mock.patch.object(api, 'request') as request:
            api.mutate('work', 'up')
            request.assert_called_with('POST', '/v1/networks/work/up')
            api.mutate('work', 'autoconnect', False)
            request.assert_called_with('PUT', '/v1/networks/work/autoconnect', {'auto_connect': False})
            api.join('work', 'laptop', 'test-secret', False, True)
            request.assert_called_with('POST', '/v1/networks/join', {'id': 'work', 'name': 'laptop', 'key': 'test-secret', 'auto_connect': False, 'connect': True})
        with self.assertRaises(gui.APIError):
            api.mutate('work', 'unknown')

    @unittest.skipUnless(hasattr(socket, 'SO_PEERCRED'), 'Linux peer credentials required')
    def test_api_transport_bounds_and_sanitizes_failures(self):
        # Fake local HTTP peer; no daemon, host DNS or network privileges.
        for status, body, expected in [(200, json.dumps([profile()]).encode(), None), (409, b'enrollment-secret', 'HTTP 409'), (200, b'x' * (gui.MAX_BODY + 1), 'size limit')]:
            with self.subTest(status=status, size=len(body)), tempfile.TemporaryDirectory(prefix='mg-', dir='/tmp') as directory:
                path = directory + '/api.sock'
                server = socket.socket(socket.AF_UNIX)
                server.bind(path)
                server.listen(1)
                requests = []
                def respond():
                    connection, _ = server.accept()
                    with connection:
                        requests.append(connection.recv(16384))
                        try:
                            connection.sendall(f'HTTP/1.1 {status} OK\r\nContent-Length: {len(body)}\r\nConnection: close\r\n\r\n'.encode() + body)
                        except BrokenPipeError:
                            pass
                thread = threading.Thread(target=respond)
                thread.start()
                try:
                    api = gui.LocalAPI(path)
                    if expected:
                        with self.assertRaises(gui.APIError) as failure:
                            api.profiles()
                        self.assertIn(expected, str(failure.exception))
                        self.assertNotIn('enrollment-secret', str(failure.exception))
                    else:
                        self.assertEqual(api.profiles()[0]['id'], 'work')
                    self.assertIn(b'Host: meldnet', requests[0])
                finally:
                    server.close()
                    thread.join(timeout=3)
                    self.assertFalse(thread.is_alive())


if __name__ == '__main__':
    unittest.main()
