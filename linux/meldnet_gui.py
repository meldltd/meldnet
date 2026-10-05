#!/usr/bin/python3
"""Unprivileged GTK frontend. All state and VPN operations belong to meldnetd."""
import argparse
import concurrent.futures
import datetime
import http.client
import json
import re
import socket
import struct
import os
from urllib.parse import quote

DEFAULT_SOCKET = '/var/run/meldnet/control.sock'
MAX_BODY = 1 << 20


class APIError(Exception):
    pass


class LocalHTTP(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__('meldnet', timeout=40)
        self.path = path

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(3)
        try:
            self.sock.connect(self.path)
            _, uid, _ = struct.unpack('3i', self.sock.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
            if uid != 0 and (self.path == DEFAULT_SOCKET or uid != os.getuid()):
                raise APIError('Control endpoint is not owned by the expected daemon account.')
            self.sock.settimeout(self.timeout)
        except Exception:
            self.sock.close()
            raise


class LocalAPI:
    def __init__(self, path):
        self.path = path

    def request(self, method, path, body=None):
        connection = LocalHTTP(self.path)
        try:
            encoded = None if body is None else json.dumps(body).encode()
            connection.request(method, path, encoded, {'Content-Type': 'application/json'} if encoded else {})
            response = connection.getresponse()
            # Never display a daemon error body: it may reflect enrollment input.
            if response.status != 200:
                raise APIError(f'Daemon rejected the request (HTTP {response.status}). Check network status before retrying.')
            data = response.read(MAX_BODY + 1)
            if len(data) > MAX_BODY:
                raise APIError('Daemon response exceeds the size limit.')
            return json.loads(data) if data.strip() else None
        except APIError:
            raise
        except (OSError, ValueError, http.client.HTTPException):
            raise APIError('Daemon unavailable or invalid response. Check the service and socket access; a timed-out operation may have completed.') from None
        finally:
            connection.close()

    def profiles(self):
        data = self.request('GET', '/v1/networks')
        if not isinstance(data, list) or len(data) > 16:
            raise APIError('Invalid network response.')
        for profile in data:
            if not isinstance(profile, dict) or not re.fullmatch(r'[a-z][a-z0-9-]{0,30}', profile.get('id', '')):
                raise APIError('Invalid network response.')
            if not isinstance(profile.get('status'), dict) or not isinstance(profile.get('network'), dict):
                raise APIError('Invalid network response.')
        return data

    def mutate(self, network, action, value=None):
        path = '/v1/networks/' + quote(network, safe='')
        if action == 'autoconnect':
            return self.request('PUT', path + '/autoconnect', {'auto_connect': bool(value)})
        if action not in ('up', 'down'):
            raise APIError('Unknown network action.')
        return self.request('POST', path + '/' + action)

    def join(self, network, name, key, auto_connect, connect):
        return self.request('POST', '/v1/networks/join', {
            'id': network, 'name': name, 'key': key,
            'auto_connect': auto_connect, 'connect': connect,
        })


def recent(stamp, seconds, now):
    try:
        value = datetime.datetime.fromisoformat(stamp.replace('Z', '+00:00')).timestamp()
        return 0 <= now - value < seconds
    except (AttributeError, ValueError, OverflowError):
        return False


def peer_rows(profile, now=None):
    now = datetime.datetime.now(datetime.timezone.utc).timestamp() if now is None else now
    status, network = profile['status'], profile['network']
    tunnel = status.get('tunnel') or {}
    peers = {p.get('public_key'): p for p in tunnel.get('peers') or []}
    own_key = (status.get('node') or {}).get('public_key')
    rows = []
    for member in network.get('members') or []:
        key = member.get('public_key')
        if status.get('backend') == 'simulation':
            state = 'Simulation'
        elif own_key and key == own_key:
            state = 'This computer'
        elif not tunnel.get('up'):
            state = 'VPN disconnected'
        elif recent(peers.get(key, {}).get('last_handshake'), 180, now):
            state = 'Recent handshake'
        elif network.get('error'):
            state = 'Directory stale'
        elif recent(member.get('last_seen'), 15, now):
            state = 'Active · control'
        else:
            state = 'No recent activity'
        rows.append((member.get('hostname') or member.get('name', 'Peer'), state, member.get('hostname')))
    return rows


def state_label(profile):
    status = profile['status']
    if status.get('backend') == 'simulation':
        return 'Simulation'
    return 'Interface up' if (status.get('tunnel') or {}).get('up') else 'Disconnected'


def main():
    parser = argparse.ArgumentParser(description='Meldnet Linux desktop interface')
    parser.add_argument('--socket', default=DEFAULT_SOCKET)
    parser.add_argument('--check', action='store_true', help='read public network/peer status without GTK')
    args = parser.parse_args()
    api = LocalAPI(args.socket)
    if args.check:
        try:
            print(json.dumps([{'id': p['id'], 'state': state_label(p), 'peers': peer_rows(p)} for p in api.profiles()]))
            return 0
        except APIError as error:
            print(str(error))
            return 1
    import gi
    gi.require_version('Gtk', '3.0')
    from gi.repository import Gtk, GLib, Gdk
    indicator_module = None
    for module in ('AyatanaAppIndicator3', 'AppIndicator3'):
        try:
            gi.require_version(module, '0.1')
            from importlib import import_module
            indicator_module = import_module('gi.repository.' + module)
            break
        except (ValueError, ImportError):
            continue

    class Desktop:
        def __init__(self):
            self.profiles = None
            self.busy = False
            self.closed = False
            self.feedback = ''
            self.executor = concurrent.futures.ThreadPoolExecutor(max_workers=1)
            self.window = Gtk.Window(title='Meldnet')
            self.window.set_default_size(520, 520)
            self.window.set_border_width(20)
            self.window.connect('delete-event', self.close_window)
            root = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=16)
            self.window.add(root)
            heading = Gtk.Label(xalign=0)
            heading.set_markup('<span size="x-large" weight="bold">Meldnet</span>')
            root.pack_start(heading, False, False, 0)
            self.message = Gtk.Label(xalign=0, wrap=True)
            root.pack_start(self.message, False, False, 0)
            scroller = Gtk.ScrolledWindow()
            scroller.set_policy(Gtk.PolicyType.NEVER, Gtk.PolicyType.AUTOMATIC)
            root.pack_start(scroller, True, True, 0)
            self.network_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=20)
            scroller.add(self.network_box)
            self.form = self.make_join_form()
            root.pack_start(self.form, False, False, 0)
            actions = Gtk.Box(spacing=8)
            self.join_button = Gtk.Button(label='Join Network…')
            self.join_button.connect('clicked', lambda _: self.show_join())
            actions.pack_start(self.join_button, False, False, 0)
            self.refresh_button = Gtk.Button(label='Refresh')
            self.refresh_button.connect('clicked', lambda _: self.refresh())
            actions.pack_start(self.refresh_button, False, False, 0)
            quit_button = Gtk.Button(label='Quit app')
            quit_button.connect('clicked', lambda _: self.quit())
            actions.pack_end(quit_button, False, False, 0)
            root.pack_start(actions, False, False, 0)
            foot = Gtk.Label(label='VPN keeps running when this app quits.', xalign=0, wrap=True)
            foot.get_style_context().add_class('dim-label')
            root.pack_start(foot, False, False, 0)
            self.indicator = None
            if indicator_module:
                self.indicator = indicator_module.Indicator.new('meldnet', 'network-vpn-symbolic', indicator_module.IndicatorCategory.APPLICATION_STATUS)
                self.indicator.set_status(indicator_module.IndicatorStatus.ACTIVE)
                self.indicator.set_title('Meldnet')
            self.window.show_all()
            self.form.hide()
            self.render()
            self.refresh()
            GLib.timeout_add_seconds(3, self.tick)

        def label(self, text):
            return Gtk.Label(label=text, xalign=0, wrap=True, selectable=True)

        def make_join_form(self):
            box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8)
            box.pack_start(self.label('Join a network'), False, False, 0)
            self.fields = []
            for label, secret in [('Network name', False), ('Device name', False), ('Enrollment key', True)]:
                entry = Gtk.Entry()
                entry.set_visibility(not secret)
                entry.set_placeholder_text(label)
                entry.get_accessible().set_name(label)
                if secret:
                    entry.set_input_purpose(Gtk.InputPurpose.PASSWORD)
                box.pack_start(entry, False, False, 0)
                self.fields.append(entry)
            self.auto = Gtk.CheckButton(label='Auto-connect at daemon startup')
            self.auto.set_active(True)
            self.connect_now = Gtk.CheckButton(label='Connect now')
            self.connect_now.set_active(True)
            box.pack_start(self.auto, False, False, 0)
            box.pack_start(self.connect_now, False, False, 0)
            actions = Gtk.Box(spacing=8)
            join = Gtk.Button(label='Join')
            join.connect('clicked', lambda _: self.submit_join())
            cancel = Gtk.Button(label='Cancel')
            cancel.connect('clicked', lambda _: self.hide_join())
            actions.pack_start(join, False, False, 0)
            actions.pack_start(cancel, False, False, 0)
            box.pack_start(actions, False, False, 0)
            return box

        def show_join(self):
            self.window.present()
            self.form.show_all()
            self.fields[0].grab_focus()

        def hide_join(self):
            for field in self.fields:
                field.set_text('')
            self.form.hide()

        def submit_join(self):
            if self.busy:
                return
            network, name, key = [field.get_text().strip() for field in self.fields]
            if not network or not name or not key:
                self.feedback = 'Enter the network name, device name and enrollment key.'
                self.render()
                return
            auto, connect = self.auto.get_active(), self.connect_now.get_active()
            self.hide_join()  # No enrollment secret persists in the frontend.
            self.request(lambda: api.join(network, name, key, auto, connect))

        def request(self, operation=None):
            if self.busy or self.closed:
                return
            self.busy = True
            self.render()
            def work():
                error = ''
                if operation:
                    try:
                        operation()
                    except APIError as failure:
                        error = str(failure)
                try:
                    return api.profiles(), error
                except APIError as failure:
                    return None, error or str(failure)
            future = self.executor.submit(work)
            def complete(future):
                try:
                    result = future.result()
                except Exception:
                    result = (None, 'Invalid daemon response. Refresh and check the service.')
                GLib.idle_add(self.completed, result, operation is not None)
            future.add_done_callback(complete)

        def completed(self, result, changed):
            if self.closed:
                return False
            self.profiles, error = result
            self.busy = False
            if error:
                self.feedback = error
            elif changed:
                self.feedback = 'Request completed.'
            else:
                self.feedback = ''
            self.render()
            return False

        def refresh(self):
            self.request()

        def tick(self):
            self.refresh()
            return not self.closed

        def copy(self, hostname):
            if hostname:
                Gtk.Clipboard.get(Gdk.SELECTION_CLIPBOARD).set_text(hostname, -1)
                self.feedback = 'Hostname copied.'
                self.message.set_text(self.feedback)

        def menu_item(self, menu, title, callback=None):
            item = Gtk.MenuItem(label=title)
            item.set_sensitive(callback is not None and not self.busy)
            if callback:
                item.connect('activate', lambda _: callback())
            menu.append(item)
            return item

        def render(self):
            for child in self.network_box.get_children():
                child.destroy()
            self.message.set_text(self.feedback or ('Updating…' if self.busy else 'Networks' if self.profiles is not None else 'Daemon unavailable. Check the background service and socket access.'))
            self.form.set_sensitive(not self.busy)
            self.join_button.set_sensitive(not self.busy and self.profiles is not None)
            self.refresh_button.set_sensitive(not self.busy)
            menu = Gtk.Menu()
            self.menu_item(menu, 'Meldnet · Networks' if self.profiles is not None else 'Meldnet · Daemon unavailable')
            for p in self.profiles or []:
                network, status = p['id'], p['status']
                up = bool((status.get('tunnel') or {}).get('up'))
                box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8)
                box.pack_start(self.label(network + ' — ' + state_label(p)), False, False, 0)
                submenu = Gtk.Menu()
                action = 'Disconnect' if up else 'Connect'
                allowed = not self.busy and status.get('node') is not None and (up or not p.get('warning'))
                callback = lambda n=network, u=up: self.request(lambda: api.mutate(n, 'down' if u else 'up'))
                button = Gtk.Button(label=action)
                button.set_sensitive(allowed)
                button.connect('clicked', lambda _, cb=callback: cb())
                controls = Gtk.Box(spacing=12)
                controls.pack_start(button, False, False, 0)
                self.menu_item(submenu, action, callback if allowed else None)
                auto = Gtk.CheckButton(label='Auto-connect at startup')
                auto.set_active(bool(p.get('auto_connect')))
                auto.set_sensitive(not self.busy)
                change_auto = lambda n=network, enabled=bool(p.get('auto_connect')): self.request(lambda: api.mutate(n, 'autoconnect', not enabled))
                auto.connect('toggled', lambda _, cb=change_auto: cb())
                controls.pack_start(auto, False, False, 0)
                box.pack_start(controls, False, False, 0)
                item = Gtk.CheckMenuItem(label='Auto-connect at startup')
                item.set_active(bool(p.get('auto_connect')))
                item.set_sensitive(not self.busy)
                item.connect('toggled', lambda _, cb=change_auto: cb())
                submenu.append(item)
                for warning in (p.get('warning'), status.get('error'), (status.get('dns') or {}).get('error'), p['network'].get('error')):
                    if warning:
                        box.pack_start(self.label(warning), False, False, 0)
                        self.menu_item(submenu, warning)
                if p['network'].get('pending'):
                    box.pack_start(self.label('Enrollment pending · retry Join with this network name'), False, False, 0)
                rows = peer_rows(p)
                for hostname, state, copyable in rows:
                    peer = Gtk.Button(label=hostname + ' — ' + state)
                    peer.set_sensitive(bool(copyable))
                    peer.set_tooltip_text('Copy hostname')
                    peer.connect('clicked', lambda _, h=copyable: self.copy(h))
                    box.pack_start(peer, False, False, 0)
                    self.menu_item(submenu, hostname + ' — ' + state, (lambda h=copyable: self.copy(h)) if copyable else None)
                if not rows:
                    box.pack_start(self.label('No peers yet'), False, False, 0)
                    self.menu_item(submenu, 'No peers yet')
                self.menu_item(submenu, 'Control activity does not prove VPN reachability')
                top = Gtk.MenuItem(label=network + ' — ' + state_label(p))
                top.set_submenu(submenu)
                menu.append(top)
                self.network_box.pack_start(box, False, False, 0)
            self.menu_item(menu, 'Open Meldnet', self.window.present)
            self.menu_item(menu, 'Join Network…', self.show_join if self.profiles is not None else None)
            self.menu_item(menu, 'Refresh', self.refresh)
            # Quit must remain available even while an API request is pending.
            quit_item = Gtk.MenuItem(label='Quit app · VPN keeps running')
            quit_item.connect('activate', lambda _: self.quit())
            menu.append(quit_item)
            menu.show_all()
            if self.indicator:
                self.indicator.set_menu(menu)
            self.network_box.show_all()

        def close_window(self, *_):
            if self.indicator:
                self.window.hide()
            else:
                self.quit()
            return True

        def quit(self):
            self.hide_join()
            self.closed = True
            self.executor.shutdown(wait=False, cancel_futures=True)
            Gtk.main_quit()

    desktop = Desktop()
    Gtk.main()
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
