#!/usr/bin/env python3
"""安装开发 CI 已固定摘要的 Linux 工具，仅提取目标二进制。"""
import argparse
import hashlib
import io
import os
from pathlib import Path
import tarfile
import urllib.request

TOOLS = {
    'gitleaks': (
        'https://github.com/gitleaks/gitleaks/releases/download/v8.30.1/gitleaks_8.30.1_linux_x64.tar.gz',
        '551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb', 'gitleaks'),
    'golangci-lint': (
        'https://github.com/golangci/golangci-lint/releases/download/v2.14.0/golangci-lint-2.14.0-linux-amd64.tar.gz',
        'ab90aeb7b066f92a33415b638a50fe5344bbb75a0d32ad30cc248d88f81032ab',
        'golangci-lint-2.14.0-linux-amd64/golangci-lint'),
}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('tool', choices=TOOLS)
    parser.add_argument('destination', type=Path)
    args = parser.parse_args()
    url, expected, member = TOOLS[args.tool]
    with urllib.request.urlopen(url, timeout=120) as response:
        raw = response.read()
    if hashlib.sha256(raw).hexdigest() != expected:
        raise SystemExit('CI tool archive checksum mismatch')
    with tarfile.open(fileobj=io.BytesIO(raw), mode='r:gz') as archive:
        info = archive.getmember(member)
        if not info.isfile():
            raise SystemExit('CI tool archive member is not a regular file')
        binary = archive.extractfile(info).read()
    args.destination.mkdir(parents=True, exist_ok=True)
    path = args.destination / args.tool
    path.write_bytes(binary)
    path.chmod(0o755)
    if os.environ.get('GITHUB_PATH'):
        with open(os.environ['GITHUB_PATH'], 'a', encoding='utf-8') as output:
            output.write(str(args.destination.resolve()) + '\n')
    print(args.tool + ': pinned archive verified')


if __name__ == '__main__':
    main()
