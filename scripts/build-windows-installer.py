#!/usr/bin/env python3
"""Build fresh-install Windows setup EXEs from verified ZIP payloads using NSIS."""
import argparse
import os
from pathlib import Path
import shutil
import struct
import subprocess
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]
FILES = {'meldnet.exe', 'meldnetd.exe', 'meldnet-gui.exe', 'wintun.dll',
         'WINTUN-LICENSE.txt', 'install.ps1', 'README.md'}
MACHINES = {'amd64': 0x8664, 'arm64': 0xaa64}


def payload(archive, arch, destination):
    """Reject unexpected/traversing entries and mismatched machine binaries."""
    with zipfile.ZipFile(archive) as z:
        if len(z.namelist()) != len(FILES) or set(z.namelist()) != FILES:
            raise ValueError('Unexpected Windows package contents')
        if any(i.file_size > 128 << 20 for i in z.infolist()):
            raise ValueError('Oversized Windows package member')
        for name in sorted(FILES):
            data = z.read(name)  # Also validates CRC, without extractall path handling.
            if name.endswith(('.exe', '.dll')):
                if len(data) < 64 or data[:2] != b'MZ':
                    raise ValueError(f'Invalid PE binary: {name}')
                offset = struct.unpack_from('<I', data, 0x3c)[0]
                if (offset + 6 > len(data) or data[offset:offset+4] != b'PE\0\0'
                        or struct.unpack_from('<H', data, offset+4)[0] != MACHINES[arch]):
                    raise ValueError(f'Wrong architecture: {name}')
            (destination / name).write_bytes(data)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--compiler', default=os.environ.get('MAKENSIS', 'makensis'))
    parser.add_argument('--docker', action='store_true', help='compile in Docker even if NSIS is installed')
    args = parser.parse_args()
    compiler = shutil.which(args.compiler)
    container = args.docker or not compiler
    if container:
        if not shutil.which('docker'):
            parser.error('Install NSIS 3 (makensis), set MAKENSIS to its path, or start Docker.')
        subprocess.run(['docker', 'build', '-t', 'meldnet-windows-installer:local',
                        '-f', str(ROOT / 'scripts/windows-installer.Dockerfile'),
                        str(ROOT / 'scripts')], check=True, timeout=600)
    for arch in MACHINES:
        # Keep staging inside bin so Docker needs only one narrowly scoped mount.
        with tempfile.TemporaryDirectory(prefix='installer-', dir=ROOT / 'bin') as tmp:
            stage = Path(tmp)
            payload(ROOT / 'bin' / f'Meldnet-windows-{arch}.zip', arch, stage)
            shutil.copyfile(ROOT / 'windows/installer.nsi', stage / 'installer.nsi')
            output = f'Meldnet-windows-{arch}-setup.exe'
            if container:
                command = ['docker', 'run', '--rm', '--network', 'none',
                           '-v', f'{stage}:/build', '-w', '/build',
                           'meldnet-windows-installer:local', '-V3',
                           '-DPAYLOAD=/build', f'-DARCH={arch}',
                           f'-DOUTPUT=/build/{output}', '/build/installer.nsi']
            else:
                prefix = '/' if os.name == 'nt' else '-'
                command = [compiler, prefix+'V3', prefix+f'DPAYLOAD={stage}',
                           prefix+f'DARCH={arch}', prefix+f'DOUTPUT={stage / output}',
                           str(stage / 'installer.nsi')]
            subprocess.run(command, check=True, timeout=300)
            target = ROOT / 'bin' / output
            os.replace(stage / output, target)
            print(target)


if __name__ == '__main__':
    main()
