#!/usr/bin/env python3
"""Create a portable desktop payload; no changes to the host system."""
from pathlib import Path
import tarfile

root = Path(__file__).resolve().parents[1]
(root / 'bin').mkdir(exist_ok=True)
output = root / 'bin' / 'Meldnet-linux-gui.tar.gz'
with tarfile.open(output, 'w:gz') as archive:
    for source, target in [
        ('linux/meldnet_gui.py', 'meldnet/meldnet_gui.py'),
        ('linux/meldnet.desktop', 'meldnet/meldnet.desktop'),
        ('linux/meldnetd.service.in', 'meldnet/meldnetd.service.in'),
        ('linux/README.md', 'meldnet/README.md'),
        ('scripts/install-linux.sh', 'meldnet/install-linux.sh'),
    ]:
        archive.add(root / source, arcname=target)
print(output)
