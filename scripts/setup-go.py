#!/usr/bin/env python3
"""Install the repository-pinned Go SDK after verifying its official checksum."""
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import tarfile
import tempfile
import urllib.request

root = Path(__file__).resolve().parent.parent
version = 'go' + (root / '.go-version').read_text().strip()
system = {'Darwin': 'darwin', 'Linux': 'linux'}.get(platform.system())
architecture = {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'amd64', 'AMD64': 'amd64'}.get(platform.machine())
if not system or not architecture:
    raise SystemExit('Install the pinned Go SDK manually on this platform.')
target = root / '.tools' / version
if (target / 'bin/go').exists():
    print(target / 'bin/go')
    raise SystemExit(0)
with urllib.request.urlopen('https://go.dev/dl/?mode=json&include=all', timeout=30) as response:
    releases = json.load(response)
release = next(item for item in releases if item['version'] == version)
artifact = next(item for item in release['files'] if item['os'] == system and item['arch'] == architecture and item['kind'] == 'archive')
(root / '.tools').mkdir(exist_ok=True)
with tempfile.TemporaryDirectory(dir=root / '.tools') as temporary:
    archive = Path(temporary) / artifact['filename']
    with urllib.request.urlopen('https://go.dev/dl/' + artifact['filename'], timeout=60) as source, archive.open('wb') as destination:
        shutil.copyfileobj(source, destination)
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    if digest != artifact['sha256']:
        raise SystemExit('Go SDK checksum verification failed')
    with tarfile.open(archive) as package:
        package.extractall(temporary, filter='data')
    (Path(temporary) / 'go').rename(target)
print(target / 'bin/go')
