#!/usr/bin/env python3
"""从精确 PR 差异生成快速测试计划；发布和手动完整运行选择全部测试。"""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[2]
MODULE = 'github.com/Wei-Shaw/sub2api/'
BACKEND = Path('backend')
DB_LAYER = ('backend/migrations/', 'backend/ent/', 'backend/internal/repository/')
SUITES = ('unit', 'integration')
PLATFORM_TAGS = {'linux', 'amd64', 'unix', 'gc', 'cgo'}
TEST_FUNC = re.compile(r'^func ((?:Test|Fuzz)\w+)\(\w+ \*testing\.[TF]\)|^func (Example\w*)\(\)', re.M)


def git(*args):
    return subprocess.check_output(['git', '-C', str(ROOT), *args]).decode()


def patterns(path):
    return [line.strip() for line in Path(path).read_text(encoding='utf-8').splitlines()
            if line.strip() and not line.lstrip().startswith('#')]


def in_suite(path, suite):
    text = path.read_text(encoding='utf-8')
    match = re.search(r'^//go:build (.+)$', text, re.M)
    if not match:
        return suite == 'unit'
    def enabled(tag):
        expr = re.sub(r'[A-Za-z0-9_.]+', lambda m: str(m[0] in PLATFORM_TAGS | {tag}), match[1])
        return bool(eval(expr.replace('&&', ' and ').replace('||', ' or ').replace('!', ' not '),
                         {'__builtins__': {}}))
    return enabled(suite) and (suite == 'unit' or not enabled('unit'))


def test_names(paths):
    return sorted({a or b for path in paths for a, b in TEST_FUNC.findall(path.read_text(encoding='utf-8'))})


def package_dir(path):
    current = path.parent
    while current != BACKEND and current != current.parent:
        if current.is_dir() and any(current.glob('*.go')):
            return current
        current = current.parent
    return None


def add_tests(packages, path, suite, whole=False):
    directory = package_dir(path)
    if directory is None:
        return
    package = MODULE + directory.relative_to(BACKEND).as_posix()
    tests = [p for p in directory.glob('*_test.go') if in_suite(p, suite)]
    if not tests:
        return
    if whole or (not path.name.endswith('_test.go') and len(tests) <= 30 and suite == 'unit'):
        packages[package] = None
        return
    if package in packages and packages[package] is None:
        return
    matches = ([path] if path.exists() and path.name.endswith('_test.go') and in_suite(path, suite)
               else [p for p in tests if p.name.startswith(path.stem + '_')])
    names = set(packages.get(package, [])) | set(test_names(matches))
    if names:
        packages[package] = sorted(names)


def slow_tests():
    result = {}
    for line in patterns('.github/ci/slow-unit-tests.txt'):
        package, name, _ = line.split()
        result.setdefault(MODULE + package, []).append(name)
    return result


def make_plan(paths, mode, commit):
    suites = {suite: {'mode': mode, 'source_commit': commit, 'suite': suite,
                      'packages': None if mode == 'full' else {},
                      'slow_tests': slow_tests() if suite == 'unit' else {}, 'shards': 4}
              for suite in SUITES}
    frontend = mode == 'full' or any(p.startswith('frontend/') for p in paths)
    if mode == 'full':
        return {'schema': 1, 'source_commit': commit, 'mode': mode, 'paths': paths,
                'suites': suites, 'frontend': True, 'frontend_tests': [], 'image': True}
    changed_go = [Path(p) for p in paths if p.startswith('backend/') and not p.endswith('.md')]
    harness = any(p.startswith(('.github/scripts/', '.github/isolation/', '.github/ci/', '.github/upgrade/'))
                  or p in {'.github/workflows/ci.yml'} for p in paths)
    dependency = any(p in {'backend/go.mod', 'backend/go.sum', 'Dockerfile'} for p in paths)
    database = any(p.startswith(DB_LAYER) for p in paths)
    references = [name for name in paths if not name.startswith('backend/') and not name.endswith('.md')]
    if references:
        for path in BACKEND.rglob('*.go'):
            text = path.read_text(encoding='utf-8')
            if any(name in text for name in references):
                changed_go.append(path)
    for path in changed_go:
        for suite in SUITES:
            add_tests(suites[suite]['packages'], path, suite, whole=not path.exists())
    # 保留开发主线的资金、认证、迁移关键路径；模块更新也走这些回归。
    if changed_go or harness or dependency:
        for pattern in patterns('.github/ci/critical-tests.txt'):
            matches = list(Path('.').glob(pattern))
            if not matches:
                raise ValueError('critical fixture missing: ' + pattern)
            for path in matches:
                add_tests(suites['unit']['packages'], path, 'unit')
                if database or harness:
                    add_tests(suites['integration']['packages'], path, 'integration')
    if database or harness:
        manifest = json.loads(Path('.github/upgrade/manifest.json').read_text())
        for item in manifest.get('test_reclassifications', []):
            package = MODULE + Path(item['path']).parent.relative_to(BACKEND).as_posix()
            suites['integration']['packages'].setdefault(package, [])
            if suites['integration']['packages'][package] is not None:
                suites['integration']['packages'][package] += item['test_functions']
        for item in manifest.get('required_integration_tests', []):
            values = suites['integration']['packages'].setdefault(item['package'], [])
            if values is not None:
                values.append(item['test'])
    for item in suites.values():
        for package, names in item['packages'].items():
            if names is not None:
                item['packages'][package] = sorted(set(names))
                if sum(map(len, names)) > 60 * 1024:
                    item['packages'][package] = None
    frontend_tests = set()
    if frontend or harness:
        frontend = True
        for pattern in patterns('.github/ci/frontend-critical-tests.txt'):
            if not (Path('frontend') / pattern).is_file():
                raise ValueError('critical frontend fixture missing: ' + pattern)
            frontend_tests.add(pattern)
        for name in paths:
            if name.startswith('frontend/') and ('.spec.' in name or '.test.' in name) and Path(name).is_file():
                frontend_tests.add(Path(name).relative_to('frontend').as_posix())
            if name.startswith('frontend/src/'):
                stem = Path(name).stem
                for path in Path('frontend/src').rglob(stem + '*.spec.ts'):
                    frontend_tests.add(path.relative_to('frontend').as_posix())
        if any(p.startswith('frontend/src/custom-portal/') for p in paths):
            frontend_tests.update(p.relative_to('frontend').as_posix()
                                  for p in Path('frontend/src/custom-portal').rglob('*.spec.ts'))
    return {'schema': 1, 'source_commit': commit, 'mode': mode, 'paths': paths,
            'suites': suites, 'frontend': frontend, 'frontend_tests': sorted(frontend_tests), 'image': False}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', default='')
    parser.add_argument('--full', action='store_true')
    parser.add_argument('--validation-root', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    commit = git('rev-parse', 'HEAD').strip()
    mode = 'full' if args.full else 'fast'
    if mode == 'fast':
        if not re.fullmatch('[0-9a-f]{40}', args.base):
            raise SystemExit('a valid PR base is required for the fast lane')
        paths = git('diff', '--name-only', '--no-renames', '-z', args.base + '...HEAD').split('\0')
        paths = sorted(filter(None, paths))
    else:
        paths = []
    os.chdir(args.validation_root)
    plan = make_plan(paths, mode, commit)
    args.output.write_text(json.dumps(plan, sort_keys=True, indent=2) + '\n', encoding='utf-8')
    outputs = {'mode': mode, 'full': mode == 'full', 'frontend': plan['frontend'], 'image': plan['image'],
               'nodes': '[20,24]' if mode == 'full' else '[24]',
               'suites': json.dumps([s for s in SUITES if mode == 'full' or plan['suites'][s]['packages']]),
               'lint_packages': './...' if mode == 'full' or 'backend/.golangci.yml' in paths else ' '.join(sorted({
                   './' + d.relative_to(BACKEND).as_posix() for name in paths if name.endswith('.go')
                   and name.startswith('backend/') and (d := package_dir(Path(name))) is not None})),
               'lint': mode == 'full' or any(p.startswith('backend/') and (p.endswith('.go') or p.endswith('.golangci.yml')) for p in paths)}
    outputs.update({suite: mode == 'full' or bool(plan['suites'][suite]['packages']) for suite in SUITES})
    if os.environ.get('GITHUB_OUTPUT'):
        with open(os.environ['GITHUB_OUTPUT'], 'a', encoding='utf-8') as handle:
            for key, value in outputs.items():
                handle.write(f'{key}={str(value).lower() if isinstance(value, bool) else value}\n')
    print(json.dumps({'mode': mode, 'changed_paths': len(paths), **outputs}, sort_keys=True))


if __name__ == '__main__':
    main()
