#!/usr/bin/env python3
"""Copy exact tracked working-tree content, then restore pinned test-only inputs.

Never copy upstream production source or overwrite repository-owned tests.
The output must not exist. It remains available on failure for investigation.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import runpy
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[2]


def git(*args):
    return subprocess.check_output(['git', '-C', str(ROOT), *args])


def test_input(path):
    p = Path(path)
    return ((path.startswith('backend/') and
             (path.endswith('_test.go') or 'testdata' in p.parts or
              path.startswith('backend/internal/testutil/') or
              path.startswith('backend/ent/enttest/'))) or
            (path.startswith('frontend/') and
             ('__tests__' in p.parts or '__mocks__' in p.parts or
              re.search(r'\.(test|spec)\.[jt]sx?$', path))) or
            path in {'frontend/vitest.config.ts', 'frontend/.eslintrc.cjs',
                     'frontend/.eslintignore', 'backend/.golangci.yml'})


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('destination', type=Path)
    args = parser.parse_args()
    dest = args.destination.resolve()
    if dest == ROOT or ROOT in dest.parents:
        parser.error('destination must be outside the repository')
    if dest.exists():
        parser.error('destination already exists; choose a fresh directory')
    manifest = json.loads((ROOT / '.github/upgrade/manifest.json').read_text())
    revisions = {manifest['test_revision'], *manifest['test_overrides'].values()}
    for rev in revisions:
        if (not re.fullmatch('[0-9a-f]{40}', rev) or
                rev not in {manifest['content_base'], manifest['target']}):
            raise SystemExit('only the fixed content baseline and stable target are allowed')
        if subprocess.run(['git', '-C', str(ROOT), 'cat-file', '-e', rev+'^{commit}'],
                          stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
            subprocess.run(['git', '-C', str(ROOT), 'fetch', '--no-tags', '--depth=1',
                            'https://github.com/Wei-Shaw/sub2api.git', rev], check=True)
    dest.mkdir(parents=True)
    owned = set(git('ls-files', '-z').decode().split('\0')) - {''}
    for package in manifest.get('absent_production_packages', {}):
        if any(p.startswith(package+'/') and p.endswith('.go') and not p.endswith('_test.go') for p in owned):
            raise SystemExit('excluded package now contains production code; restore its tests: '+package)
    source_hashes = {}
    for path in sorted(owned):
        source = ROOT / path
        if source.is_symlink() or not source.is_file():
            raise SystemExit('tracked file missing or not regular: '+path)
        output = dest / path
        output.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, output)
        source_hashes[path] = hashlib.sha256(source.read_bytes()).hexdigest()
    selected = {}
    for rev in revisions:
        for path in git('ls-tree', '-r', '--name-only', rev).decode().splitlines():
            if any(path.startswith(package+'/') for package in manifest.get('absent_production_packages', {})):
                continue
            if test_input(path) and manifest['test_overrides'].get(path, manifest['test_revision']) == rev:
                selected[path] = rev
    restored = []
    for path, rev in sorted(selected.items()):
        if path in owned:
            continue
        output = dest / path
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_bytes(git('show', rev+':'+path))
        restored.append({'path': path, 'revision': rev})
    support = []
    for destination, spec in manifest.get('validation_support', {}).items():
        source = spec['source']
        if (not destination.startswith('backend/internal/isolatedfixture/') or
                not source.startswith('.github/upgrade/fixtures/') or
                '..' in Path(destination).parts or '..' in Path(source).parts):
            raise SystemExit('invalid isolated support path')
        if destination in owned or (dest / destination).exists():
            raise SystemExit('support would overwrite existing source: '+destination)
        raw = (ROOT / source).read_bytes()
        digest = hashlib.sha256(raw).hexdigest()
        if digest != spec['sha256']:
            raise SystemExit('unreviewed isolated support bytes: '+source)
        output = dest / destination
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_bytes(raw)
        support.append({'source': source, 'destination': destination, 'sha256': digest})
    adaptations = []
    adapter = ROOT / '.github/upgrade/adapt-tests.py'
    adapt = runpy.run_path(str(adapter))['adapt']
    for path, hashes in manifest.get('test_adaptations', {}).items():
        if path in owned:
            raise SystemExit('adaptation would overwrite repository-owned test: '+path)
        output = dest / path
        original = output.read_bytes()
        before = hashlib.sha256(original).hexdigest()
        if before != hashes[selected[path]]:
            raise SystemExit('unreviewed upstream test bytes: '+path)
        output.write_text(adapt(path, original.decode()))
        adaptations.append({'path': path, 'before_sha256': before,
                            'after_sha256': hashlib.sha256(output.read_bytes()).hexdigest()})
    receipt = {'tested_commit': git('rev-parse', 'HEAD').decode().strip(),
               'working_tree_diff': git('diff', 'HEAD', '--stat').decode(),
               'production_source': 'tracked working tree, never upstream',
               'source_sha256': source_hashes,
               'excluded_absent_packages': manifest.get('absent_production_packages', {}),
               'restored': restored, 'adaptations': adaptations,
               'validation_support': support,
               'test_reclassifications': manifest.get('test_reclassifications', []),
               'adapter_sha256': {p.name: hashlib.sha256(p.read_bytes()).hexdigest()
                                  for p in sorted(adapter.parent.glob('adapt-*.py'))}}
    (dest / 'validation-receipt.json').write_text(json.dumps(receipt, indent=2)+'\n')
    print(f'Restored {len(restored)} pinned test/support files onto {len(owned)} repository files: {dest}')


if __name__ == '__main__':
    main()
