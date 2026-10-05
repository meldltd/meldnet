#!/usr/bin/env python3
"""Package CGO-free binaries with checksum-pinned official signed Wintun DLLs."""
from pathlib import Path
import hashlib
import io
import urllib.request
import zipfile

root = Path(__file__).resolve().parents[1]
url = 'https://www.wintun.net/builds/wintun-0.14.1.zip'
sha256 = '07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51'
cache = root / 'bin' / 'wintun-0.14.1.zip'
if cache.exists():
    data = cache.read_bytes()
else:
    with urllib.request.urlopen(url, timeout=30) as response:
        data = response.read(8 << 20)
if hashlib.sha256(data).hexdigest() != sha256:
    raise SystemExit('Official Wintun archive checksum mismatch')
cache.write_bytes(data)
with zipfile.ZipFile(io.BytesIO(data)) as driver:
    for arch in ('amd64', 'arm64'):
        target = root / 'bin' / f'Meldnet-windows-{arch}.zip'
        with zipfile.ZipFile(target, 'w', zipfile.ZIP_DEFLATED) as archive:
            for binary in ('meldnet.exe', 'meldnetd.exe'):
                archive.write(root / 'bin' / f'windows-{arch}' / binary, binary)
            archive.writestr('wintun.dll', driver.read(f'wintun/bin/{arch}/wintun.dll'))
            archive.writestr('WINTUN-LICENSE.txt', driver.read('wintun/LICENSE.txt'))
            archive.write(root / 'windows' / 'install.ps1', 'install.ps1')
            archive.write(root / 'windows' / 'README.md', 'README.md')
        print(target)
