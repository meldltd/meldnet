#!/usr/bin/env python3
"""Build mobile-only JNI libraries; desktop releases remain CGO disabled."""
import os
from pathlib import Path
import platform
import subprocess

root = Path(__file__).resolve().parents[1]
sdk = Path(os.environ.get('ANDROID_HOME', Path.home() / 'Library/Android/sdk'))
ndk = Path(os.environ.get('ANDROID_NDK_HOME', sdk / 'ndk/28.2.13676358'))
host = 'darwin-x86_64' if platform.system() == 'Darwin' else 'linux-x86_64'
compiler = ndk / 'toolchains/llvm/prebuilt' / host / 'bin'
for arch, abi, triple in [('arm64', 'arm64-v8a', 'aarch64-linux-android'),
                          ('amd64', 'x86_64', 'x86_64-linux-android')]:
    cc = compiler / f'{triple}29-clang'
    if not cc.exists():
        raise SystemExit('Android NDK 28.2.13676358 required; set ANDROID_NDK_HOME/ANDROID_HOME.')
    output = root / 'mobile/native/build/jniLibs' / abi / 'libwg-go.so'
    output.parent.mkdir(parents=True, exist_ok=True)
    env = dict(os.environ, GOOS='android', GOARCH=arch, CGO_ENABLED='1', CC=str(cc),
               CGO_LDFLAGS='-Wl,-z,max-page-size=16384')
    subprocess.run(['go', 'build', '-trimpath', '-buildmode=c-shared', '-ldflags=-s -w',
                    '-o', str(output), './cmd/wgmobile'], cwd=root / 'mobile/native',
                   env=env, check=True, timeout=600)
    print(output)
