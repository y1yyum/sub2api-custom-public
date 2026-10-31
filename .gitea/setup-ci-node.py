#!/usr/bin/env python3
"""Install only the checksum-pinned Node executable into this CI job's PATH."""
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import urllib.request

VERSION = '24.15.0'
MAX_ARCHIVE = 128 * 1024 * 1024
MAX_EXECUTABLE = 256 * 1024 * 1024


def load_checksum(path, filename):
    matches = [line.split()[0] for line in path.read_text(encoding='utf-8').splitlines()
               if not line.startswith('#') and len(line.split()) == 2 and line.split()[1] == filename]
    if len(matches) != 1 or not re.fullmatch(r'[0-9a-f]{64}', matches[0]):
        raise ValueError('exact Node checksum pin required')
    return matches[0]


def extract_verified(archive, expected, filename, destination):
    if (not re.fullmatch(r'node-v' + re.escape(VERSION) + r'-linux-(?:x64|arm64)\.tar\.gz', filename) or
            not re.fullmatch(r'[0-9a-f]{64}', expected) or
            not archive.is_file() or archive.is_symlink()):
        raise ValueError('invalid pinned Node archive')
    checksum = hashlib.sha256()
    with archive.open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            checksum.update(block)
    if archive.stat().st_size > MAX_ARCHIVE or checksum.hexdigest() != expected:
        raise ValueError('Node archive checksum mismatch')
    if destination.exists() or destination.is_symlink() or destination.parent.is_symlink():
        raise ValueError('fresh private Node destination required')
    wanted = filename.removesuffix('.tar.gz') + '/bin/node'
    with tarfile.open(archive, 'r:gz') as bundle:
        members = [member for member in bundle.getmembers() if member.name == wanted]
        if len(members) != 1 or not members[0].isfile() or not 0 < members[0].size <= MAX_EXECUTABLE:
            raise ValueError('regular pinned Node executable required')
        (destination / 'bin').mkdir(mode=0o700, parents=True)
        binary = destination / 'bin/node'
        with bundle.extractfile(members[0]) as source, binary.open('xb') as output:
            shutil.copyfileobj(source, output)
        binary.chmod(0o700)
    return binary


def main():
    architecture = {'x86_64': 'x64', 'aarch64': 'arm64'}.get(platform.machine())
    if platform.system() != 'Linux' or architecture is None:
        raise ValueError('supported Linux Node architecture required')
    temporary, path_file = os.environ.get('RUNNER_TEMP'), os.environ.get('GITHUB_PATH')
    if not temporary or not path_file:
        raise ValueError('CI temporary directory and PATH command file required')
    filename = 'node-v' + VERSION + '-linux-' + architecture + '.tar.gz'
    expected = load_checksum(Path(__file__).with_name('node-shasums.txt'), filename)
    work = Path(tempfile.mkdtemp(prefix='sub2api-node-', dir=temporary))
    archive = work / filename
    url = 'https://nodejs.org/dist/v' + VERSION + '/' + filename
    with urllib.request.urlopen(url, timeout=60) as source, archive.open('xb') as output:
        size = 0
        while block := source.read(1024 * 1024):
            size += len(block)
            if size > MAX_ARCHIVE:
                raise ValueError('Node archive size limit exceeded')
            output.write(block)
    binary = extract_verified(archive, expected, filename, work / 'runtime')
    result = subprocess.run([str(binary), '--version'], capture_output=True, text=True, check=True, timeout=10)
    if result.stdout != 'v' + VERSION + '\n':
        raise ValueError('pinned Node executable version mismatch')
    bindir = str(binary.parent)
    if '\n' in bindir or '\r' in bindir:
        raise ValueError('invalid Node PATH directory')
    with Path(path_file).open('a', encoding='utf-8') as output:
        output.write(bindir + '\n')
    archive.unlink()
    print(json.dumps({'ci_node': VERSION, 'architecture': architecture, 'archive_sha256': expected}), flush=True)
    return 0


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception as error:
        print(json.dumps({'ci_node': VERSION, 'status': 'failed', 'error_type': type(error).__name__}),
              file=sys.stderr)
        raise SystemExit(1)
