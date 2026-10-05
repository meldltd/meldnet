#!/usr/bin/python3
"""Native GTK interaction smoke test with a fake API; run under Xvfb on Linux."""
import importlib.util
import sys
import datetime
from pathlib import Path
import gi

gi.require_version('Gtk', '3.0')
from gi.repository import Gtk, GLib, Gdk
root = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('gui', root / 'linux/meldnet_gui.py')
gui = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gui)


class FakeAPI:
    def __init__(self, _):
        self.networks = [{'id': 'work', 'auto_connect': True, 'status': {'backend': 'simulation', 'node': {'public_key': 'test-public'}, 'tunnel': {'up': True, 'peers': []}}, 'network': {'members': [{'name': 'peer', 'public_key': 'peer-public', 'hostname': 'peer.work.meldnet.internal'}]}}]
        self.fail = False
        self.calls = []

    def profiles(self):
        if self.fail:
            raise gui.APIError('Daemon unavailable. Check socket access.')
        return self.networks

    def mutate(self, network, action, value=None):
        assert network == 'work'
        self.calls.append((action, value))
        if action == 'down':
            self.networks[0]['status']['tunnel']['up'] = False
        elif action == 'autoconnect':
            self.networks[0]['auto_connect'] = value
        else:
            raise AssertionError(action)

    def join(self, network, name, key, auto, connect):
        assert (network, name, key, auto, connect) == ('home', 'laptop', 'synthetic-enrollment', True, True)
        self.calls.append(('join', network))
        self.networks.append({'id': network, 'auto_connect': auto, 'status': {'backend': 'simulation', 'node': {'public_key': 'other-test-public'}, 'tunnel': {'up': connect, 'peers': []}}, 'network': {'members': []}})


api = FakeAPI('unused')
gui.LocalAPI = lambda _: api
original_main = Gtk.main
phase = 0
ticks = 0
failure = []
output = Path(sys.argv[1]) if len(sys.argv) > 1 else root / 'bin' / 'linux-gui.png'
output.parent.mkdir(parents=True, exist_ok=True)
sys.argv = ['meldnet_gui.py']


def descendants(widget):
    yield widget
    if isinstance(widget, Gtk.Container):
        for child in widget.get_children():
            yield from descendants(child)


def check():
    global phase, ticks
    try:
        ticks += 1
        if ticks > 300:
            raise AssertionError(f'GTK smoke timed out at phase {phase}')
        windows = [w for w in Gtk.Window.list_toplevels() if w.get_title() == 'Meldnet']
        if not windows:
            return True
        window = windows[0]
        widgets = list(descendants(window))
        labels = [w.get_text() for w in widgets if isinstance(w, Gtk.Label)]
        buttons = {w.get_label(): w for w in widgets if isinstance(w, Gtk.Button)}
        if phase == 0 and 'Disconnect' in buttons and buttons['Disconnect'].get_sensitive():
            assert 'work — Simulation' in labels
            assert 'peer.work.meldnet.internal — Simulation' in buttons
            buttons['peer.work.meldnet.internal — Simulation'].clicked()
            buttons['Disconnect'].clicked()
            phase = 1
        elif phase == 1 and 'Connect' in buttons and buttons['Connect'].get_sensitive():
            assert api.networks[0]['auto_connect'] is True  # Disconnect must not change startup.
            buttons['Auto-connect at startup'].set_active(False)
            phase = 2
        elif phase == 2 and api.networks[0]['auto_connect'] is False and buttons['Join Network…'].get_sensitive():
            buttons['Join Network…'].clicked()
            entries = {w.get_placeholder_text(): w for w in widgets if isinstance(w, Gtk.Entry)}
            assert not entries['Enrollment key'].get_visibility()
            for name, value in [('Network name', 'home'), ('Device name', 'laptop'), ('Enrollment key', 'synthetic-enrollment')]:
                entries[name].set_text(value)
            phase = 2.5
        elif phase == 2.5:
            entries = {w.get_placeholder_text(): w for w in widgets if isinstance(w, Gtk.Entry)}
            image = Gdk.pixbuf_get_from_window(window.get_window(), 0, 0, window.get_allocated_width(), window.get_allocated_height())
            image.savev(str(output.with_name('linux-gui-join.png')), 'png', [], [])
            buttons['Join'].clicked()
            assert entries['Enrollment key'].get_text() == ''
            phase = 3
        elif phase == 3 and 'home — Simulation' in labels and buttons['Refresh'].get_sensitive():
            assert ('down', None) in api.calls and ('autoconnect', False) in api.calls and ('join', 'home') in api.calls
            image = Gdk.pixbuf_get_from_window(window.get_window(), 0, 0, window.get_allocated_width(), window.get_allocated_height())
            image.savev(str(output), 'png', [], [])
            api.fail = True
            buttons['Refresh'].clicked()
            phase = 4
        elif phase == 4 and 'Daemon unavailable. Check socket access.' in labels and buttons['Refresh'].get_sensitive():
            assert not any('work —' in label or 'home —' in label for label in labels)
            assert not buttons['Join Network…'].get_sensitive()
            buttons['Quit app'].clicked()
            phase = 5
            return False
        return True
    except Exception as error:
        failure.append(error)
        Gtk.main_quit()
        return False


def smoke_loop():
    GLib.timeout_add(50, check)
    original_main()


Gtk.main = smoke_loop
gui.main()
if failure:
    raise failure[0]
assert phase == 5, phase
assert len(api.networks) == 2  # Frontend quit made no disconnect/daemon-stop call.
print('PASS: GTK connect/startup/join masking/copy/failure snapshot/quit isolation; ' + str(output))
