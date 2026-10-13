"""Packaging tests; no service, installer, routes or host DNS are touched."""
import importlib.util
from pathlib import Path
import struct
import tempfile
import unittest
import warnings
import zipfile

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('installer', ROOT / 'scripts/build-windows-installer.py')
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


def pe(machine):
    data = bytearray(128)
    data[:2] = b'MZ'
    struct.pack_into('<I', data, 0x3c, 64)
    data[64:68] = b'PE\0\0'
    struct.pack_into('<H', data, 68, machine)
    return bytes(data)


class PayloadTests(unittest.TestCase):
    def package(self, root, machine=0x8664, extra=None, bad=None):
        path = root / 'package.zip'
        with zipfile.ZipFile(path, 'w') as z:
            for name in installer.FILES:
                data = pe(machine) if name.endswith(('.exe', '.dll')) else b'fixture'
                if name == bad:
                    data = b'not a PE'
                z.writestr(name, data)
            if extra:
                with warnings.catch_warnings():
                    warnings.simplefilter('ignore', UserWarning)
                    z.writestr(extra, b'unsafe')
        return path

    def test_both_architectures(self):
        for arch, machine in installer.MACHINES.items():
            with self.subTest(arch=arch), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                out = root / 'payload'
                out.mkdir()
                installer.payload(self.package(root, machine), arch, out)
                self.assertEqual({p.name for p in out.iterdir()}, installer.FILES)

    def test_rejects_traversal_and_extra_files_before_extraction(self):
        for extra in ('../outside', 'unexpected.exe', 'meldnet.exe'):
            with self.subTest(extra=extra), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                out = root / 'payload'
                out.mkdir()
                with self.assertRaises(ValueError):
                    installer.payload(self.package(root, extra=extra), 'amd64', out)
                self.assertEqual(list(out.iterdir()), [])

    def test_wrong_architecture_or_invalid_binary(self):
        for machine, bad in ((0xaa64, None), (0x8664, 'wintun.dll')):
            with self.subTest(machine=machine, bad=bad), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                out = root / 'payload'
                out.mkdir()
                with self.assertRaises(ValueError):
                    installer.payload(self.package(root, machine, bad=bad), 'amd64', out)


if __name__ == '__main__':
    unittest.main()
