#!/usr/bin/env python3
"""Inspect the built archive and sandbox installer scripts. Never install on host."""
import json
import os
from pathlib import Path
import plistlib
import shutil
import subprocess
import tempfile
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parent.parent
VERSION = os.environ.get('VERSION', '0.1.0')
PKG = ROOT / f'bin/Meldnet-{VERSION}-universal.pkg'


def command(*args):
    return subprocess.check_output(args, text=True).strip()


with tempfile.TemporaryDirectory(prefix='meldnet-pkg-test-') as temp:
    expanded = Path(temp) / 'expanded'
    subprocess.run(['pkgutil', '--expand-full', str(PKG), str(expanded)], check=True)
    distribution = ET.parse(expanded / 'Distribution').getroot()
    domains = distribution.find('domains')
    assert domains.get('enable_localSystem') == 'true'
    assert domains.get('enable_currentUserHome') == 'false'
    assert distribution.find('pkg-ref').get('onConclusion') == 'None'
    assert distribution.find('pkg-ref').get('auth') == 'Root'
    assert distribution.find('volume-check/allowed-os-versions/os-version').get('min') == '13.0'
    component = expanded / 'Meldnet-component.pkg'
    payload = component / 'Payload'
    binaries = ['Applications/Meldnet.app/Contents/MacOS/Meldnet',
                'Applications/Meldnet.app/Contents/Resources/meldnet',
                'Library/PrivilegedHelperTools/si.meldnet.daemon']
    bom = command('lsbom', '-p', 'fmug', str(component / 'Bom'))
    entries = {line.split('\t')[0].removeprefix('./'): line.split('\t')[1:] for line in bom.splitlines()}
    for binary in binaries:
        assert set(command('lipo', '-archs', str(payload / binary)).split()) == {'arm64', 'x86_64'}
        mode, uid, gid = entries[binary]
        assert int(mode, 8) & 0o7777 == 0o755, (binary, mode)
        assert (uid, gid) == ('0', '0'), (binary, uid, gid)
    subprocess.run(['codesign', '--verify', '--strict', '--deep', str(payload / 'Applications/Meldnet.app')], check=True)
    subprocess.run(['codesign', '--verify', '--strict', str(payload / binaries[2])], check=True)
    info = plistlib.loads((payload / 'Applications/Meldnet.app/Contents/Info.plist').read_bytes())
    assert info['CFBundleShortVersionString'] == VERSION
    assert info['LSMinimumSystemVersion'] == '13.0'
    assert not any('Application Support/Meldnet' in path or 'network.json' in path or 'key.txt' in path for path in entries)
    metadata = ET.parse(component / 'PackageInfo').getroot()
    assert metadata.find('relocate') is None or len(metadata.find('relocate')) == 0
    for name in ['preinstall', 'postinstall', 'common.sh', 'daemon.plist.in', 'agent.plist.in']:
        assert (component / 'Scripts' / name).read_bytes() == (ROOT / 'packaging/macos/scripts' / name).read_bytes()
    # Wrong-account LaunchAgents exit before contacting the daemon.
    app = str(payload / binaries[0])
    result = subprocess.run([app, '--only-uid', str(os.getuid() + 1), '--socket', '/nonexistent/meldnet.sock', '--check'], capture_output=True)
    assert result.returncode == 0 and result.stdout == b''
    result = subprocess.run([app, '--only-uid', str(os.getuid()), '--socket', '/nonexistent/meldnet.sock', '--check'], capture_output=True)
    assert result.returncode == 1
print('PASS: package authorization/no-restart, universal binaries, root ownership/modes, signatures, fixed app destination, no state/keys, UID login gating')

# Run the actual shipped shell logic against temporary destinations. Only OS
# privilege/account lookups, chown/install ownership, and launchctl are mocked.
# No fixture switch or path override exists in production installer scripts.
with tempfile.TemporaryDirectory(prefix='meldnet-install-sandbox-') as temp:
    base = Path(temp)
    fixture = base / 'root'
    scripts = base / 'scripts'
    scripts.mkdir()
    config = base / 'model.json'
    calls = base / 'calls'
    shim = base / 'os-shim'
    shim.write_text('''#!/usr/bin/env python3
import json, os, pathlib, subprocess, sys
cfg = json.loads(pathlib.Path(os.environ['MODEL']).read_text())
a = sys.argv[1:]; tool = a.pop(0)
if tool == 'stat':
    if a[1] == '%u':
        print(cfg.get('console', 501) if a[2].endswith('/dev/console') else cfg.get('owners', {}).get(a[2], 0))
    else: sys.exit(subprocess.call(['/usr/bin/stat'] + a))
elif tool == 'id':
    if a == ['-u']: print(cfg.get('euid', 0))
    elif a[0] == '-un' and a[1] in ['501', '502']: print('fixture-user')
    else: sys.exit(1)
elif tool == 'install':
    pathlib.Path(a[-1]).mkdir(parents=True, exist_ok=True)
    os.chmod(a[-1], 0o700)
elif tool == 'launchctl':
    with open(os.environ['CALLS'], 'a') as f: f.write(' '.join(a) + '\\n')
    if a[0] == 'print': sys.exit(0 if a[1] in cfg.get('loaded', ['gui/501']) else 1)
    if a[0] == 'bootstrap' and cfg.get('bootstrap_failure'): sys.exit(5)
elif tool != 'chown': sys.exit(2)
''')
    shim.chmod(0o755)
    for source in (ROOT / 'packaging/macos/scripts').iterdir():
        text = source.read_text()
        if source.suffix != '.in':
            for path in ['/Library', '/Applications', '/dev/console']:
                text = text.replace(path, str(fixture) + path)
            for tool in ['/usr/bin/stat', '/usr/bin/id', '/usr/sbin/chown', '/usr/bin/install', '/bin/launchctl']:
                text = text.replace(tool, f'"{shim}" {Path(tool).name}')
            text = text.replace('! -user root', f'! -user {os.getuid()}')
        (scripts / source.name).write_text(text)
    env = dict(os.environ, MODEL=str(config), CALLS=str(calls))

    def reset(model=None):
        if fixture.exists():
            shutil.rmtree(fixture)
        for path in ['Library/PrivilegedHelperTools', 'Library/LaunchDaemons', 'Library/LaunchAgents', 'Library/Logs', 'Applications']:
            (fixture / path).mkdir(parents=True, exist_ok=True)
        config.write_text(json.dumps(model or {}))
        calls.write_text('')

    def run(script, ok=True, target='/'):
        result = subprocess.run(['/bin/bash', str(scripts / script), 'fixture.pkg', '/', target], env=env, capture_output=True, text=True)
        assert (result.returncode == 0) == ok, (script, result.stdout, result.stderr)
        return result

    reset()
    run('preinstall')
    # Simulate Installer extracting its root-owned payload before postinstall.
    (fixture / 'Library/PrivilegedHelperTools/si.meldnet.daemon').write_text('fixture')
    run('postinstall')
    daemon_path = fixture / 'Library/LaunchDaemons/si.meldnet.daemon.plist'
    daemon = plistlib.loads(daemon_path.read_bytes())
    assert daemon['ProgramArguments'] == ['/Library/PrivilegedHelperTools/si.meldnet.daemon', '--allow-uid', '501']
    agent = plistlib.loads((fixture / 'Library/LaunchAgents/si.meldnet.menubar.pkg.plist').read_bytes())
    assert agent['ProgramArguments'][-2:] == ['--only-uid', '501']
    assert daemon_path.stat().st_mode & 0o777 == 0o644
    assert (fixture / 'Library/Logs/Meldnet/daemon.log').stat().st_mode & 0o777 == 0o600
    assert calls.read_text().splitlines() == [
        'enable system/si.meldnet.daemon', 'print system/si.meldnet.daemon',
        'bootstrap system ' + str(daemon_path), 'print gui/501',
        'enable gui/501/si.meldnet.menubar.pkg', 'print gui/501/si.meldnet.menubar.pkg',
        'bootstrap gui/501 ' + str(fixture / 'Library/LaunchAgents/si.meldnet.menubar.pkg.plist')]
    assert not any(word in calls.read_text() for word in ['bootout', 'kickstart', 'kill'])
    # Console-user changes do not silently transfer control during upgrades.
    config.write_text(json.dumps({'console': 502, 'loaded': ['system/si.meldnet.daemon', 'gui/501', 'gui/501/si.meldnet.menubar.pkg']}))
    calls.write_text('')
    # Rewrite only the test root path in an existing plist for path comparisons.
    daemon['ProgramArguments'][0] = str(fixture) + '/Library/PrivilegedHelperTools/si.meldnet.daemon'
    daemon_path.write_bytes(plistlib.dumps(daemon))
    run('preinstall')
    run('postinstall')
    assert plistlib.loads(daemon_path.read_bytes())['ProgramArguments'][-1] == '501'
    assert 'bootstrap' not in calls.read_text()
    # Custom daemon arguments are never discarded.
    daemon['ProgramArguments'] += ['--data-dir', '/custom']
    daemon_path.write_bytes(plistlib.dumps(daemon))
    run('preinstall', ok=False)
    reset({'bootstrap_failure': True})
    (fixture / 'Library/PrivilegedHelperTools/si.meldnet.daemon').write_text('fixture')
    run('postinstall', ok=False)
    reset({'loaded': []})
    (fixture / 'Library/PrivilegedHelperTools/si.meldnet.daemon').write_text('fixture')
    run('postinstall')
    assert 'bootstrap system' in calls.read_text() and 'bootstrap gui/' not in calls.read_text()
    for model in [{'euid': 501}, {'console': 0}, {'console': 99999}]:
        reset(model)
        run('preinstall', ok=False)
    reset()
    run('preinstall', ok=False, target='/Volumes/Other')
    for unsafe in ['symlink', 'writable', 'owner', 'hardlink']:
        reset()
        helper = fixture / 'Library/PrivilegedHelperTools/si.meldnet.daemon'
        if unsafe == 'symlink': helper.symlink_to(base / 'redirect')
        else:
            helper.write_text('fixture')
            if unsafe == 'writable': helper.chmod(0o777)
            if unsafe == 'owner': config.write_text(json.dumps({'owners': {str(helper): 501}}))
            if unsafe == 'hardlink': os.link(helper, fixture / 'linked')
        run('preinstall', ok=False)
    reset()
    bundle = fixture / 'Applications/Meldnet.app'
    bundle.mkdir()
    (bundle / 'redirect').symlink_to(base)
    run('preinstall', ok=False)
print('PASS: sandbox fresh install/upgrade, UID preservation, private logs, immediate startup, existing jobs preserved, bootstrap failure, absent GUI session, no stop, non-root/offline/no-user/custom-config rejection, symlink/hardlink/ownership/mode protections')
