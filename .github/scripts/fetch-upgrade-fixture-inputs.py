#!/usr/bin/env python3
"""Fetch only the two reviewed public fixture commits before any static checks."""
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[2]
PINNED = ('01842576b3d5c1e2545b5a99ad555d36e28ed306',
          'ac1b8f1271be0d674fa6f074403c1410f30a3a81')
UPSTREAM = 'https://github.com/Wei-Shaw/sub2api.git'


def fetch_inputs(root=ROOT, manifest=None):
    if manifest is None:
        manifest = json.loads((root / '.github/upgrade/manifest.json').read_text())
    if (manifest['content_base'], manifest['target']) != PINNED:
        raise RuntimeError('fixture revisions differ from reviewed fixed baseline/target')
    for revision in PINNED:
        if subprocess.run(['git', '-C', str(root), 'cat-file', '-e', revision+'^{commit}'],
                          stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
            subprocess.run(['git', '-C', str(root), 'fetch', '--no-tags', '--depth=1',
                            UPSTREAM, revision], check=True)
        actual = subprocess.check_output(['git', '-C', str(root), 'rev-parse', revision+'^{commit}'], text=True).strip()
        if actual != revision:
            raise RuntimeError('fetched fixture commit identity mismatch')


if __name__ == '__main__':
    fetch_inputs()
