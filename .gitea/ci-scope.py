#!/usr/bin/env python3
"""Decide which Local CI jobs a pull request needs, from its Git diff.

Pull requests take the fast lane: Go tests named after the changed files
plus the fixed critical list in .gitea/critical-tests.txt. Release tag
pushes, manual runs, unknown ranges, job image and module changes run every
test; workflow edits run the jobs they touch in full.
"""
import argparse
import os
from pathlib import Path
import re
import subprocess
import yaml

ALL = './...'
BACKEND = Path('backend')
FULL_PREFIXES = ('deploy/local-gitea/ci-image/', '.github/actions/setup-ci/')
FULL_FILES = {'backend/go.mod', 'backend/go.sum', 'backend/Makefile'}
WORKFLOW = os.environ.get('CI_WORKFLOW', '.gitea/workflows/local-ci.yml')
CRITICAL = '.gitea/critical-tests.txt'
UNIT_FULL = ('.gitea/unit-full.sh', '.gitea/slow-unit-tests.txt')
NODE_SETUP = ('.gitea/setup-ci-node.py', '.gitea/node-shasums.txt')
DB_LAYER = ('backend/migrations/', 'backend/ent/', 'backend/internal/repository/')
JOB_OUTPUTS = {'unit': 'unit', 'integration': 'integration', 'golangci-lint': 'lint',
               'frontend': 'frontend', 'shell': 'shell', 'release-helpers': 'release'}
GO_TAGS = ('unit', 'integration')
PLATFORM_TAGS = {'linux', 'amd64', 'unix', 'gc', 'cgo'}
# Larger packages (service, repository, handler) run only the test files
# named after the changed files; smaller ones run all their unit tests.
WHOLE_PACKAGE_MAX_TEST_FILES = 30
# Linux caps one argument at 128 KiB; past this the selected packages run whole.
MAX_RUN_PATTERN = 64 * 1024
TEST_FUNC = re.compile(r'^func (Test\w+)\(\w+ \*testing\.T\)', re.M)


def git_lines(*args):
    out = subprocess.run(['git', *args], capture_output=True, check=True).stdout
    return [line for line in out.decode().split('\0') if line]


def build_tag_matches(path, tag):
    """Evaluate a test file's //go:build line for the given tag."""
    try:
        lines = path.read_text(encoding='utf-8', errors='replace').splitlines()
    except FileNotFoundError:
        return True
    for line in lines:
        if line.startswith('//go:build '):
            expr = line[len('//go:build '):]
            enabled = PLATFORM_TAGS | {tag}
            py = re.sub(r'[A-Za-z0-9_.]+', lambda m: str(m.group(0) in enabled), expr)
            py = py.replace('&&', ' and ').replace('||', ' or ').replace('!', ' not ')
            return bool(eval(py, {'__builtins__': {}}))
        if line.startswith('package '):
            return True
    return True


def in_suite(path, tag):
    """Untagged tests compile under both tags; count them in unit only so the
    integration job runs just the tests that need its database."""
    if tag == 'unit':
        return build_tag_matches(path, 'unit')
    return build_tag_matches(path, 'integration') and not build_tag_matches(path, 'unit')


def test_names(paths):
    names = set()
    for path in paths:
        names.update(TEST_FUNC.findall(path.read_text(encoding='utf-8', errors='replace')))
    return names


def package_dir(path):
    """Nearest directory at or above path that holds Go files."""
    current = path.parent
    while current != BACKEND and current != current.parent:
        if current.is_dir() and any(current.glob('*.go')):
            return current
        current = current.parent
    return None


def package_arg(directory):
    return './' + directory.relative_to(BACKEND).as_posix()


def select(whole, named, tag, directory, path):
    """Record the tests suite tag needs for one changed file. named maps a
    package to test names; an empty set still compiles its tests."""
    package = package_arg(directory)
    if path.name.endswith('_test.go'):
        if not path.exists():
            if tag == 'unit':
                named.setdefault(package, set())
        elif in_suite(path, tag):
            named.setdefault(package, set()).update(test_names([path]))
        return
    tests = [p for p in directory.glob('*_test.go') if in_suite(p, tag)]
    if not tests:
        return
    small = len(list(directory.glob('*_test.go'))) <= WHOLE_PACKAGE_MAX_TEST_FILES
    if small and tag == 'unit':
        whole.add(package)
        return
    stem = path.name.split('.')[0]
    names = test_names(tests if small else [p for p in tests if p.name.startswith(stem + '_')])
    if names or tag == 'unit':
        named.setdefault(package, set()).update(names)


def critical_files():
    lines = Path(CRITICAL).read_text(encoding='utf-8').splitlines()
    patterns = [line.strip() for line in lines if line.strip() and not line.startswith('#')]
    return sorted({path for pattern in patterns for path in Path('.').glob(pattern)})


def go_test_outputs(tag, whole, named):
    selected = {pkg: names for pkg, names in named.items() if pkg not in whole}
    names = sorted(set().union(*selected.values()))
    run = f'^({"|".join(names)})$' if names else '^$'
    if len(run) > MAX_RUN_PATTERN:
        whole, selected = whole | set(selected), {}
    return {tag: ' '.join(sorted(whole)),
            f'{tag}_selected': ' '.join(sorted(selected)),
            f'{tag}_run': run if selected else ''}


def go_references(name):
    """Backend Go files that name a non-backend file, e.g. tests reading
    frontend/src/constants/platformCatalog.builtin.json."""
    return [path for path in BACKEND.rglob('*.go')
            if name in path.read_text(encoding='utf-8', errors='replace')]


def changed_jobs(base):
    """Scoped outputs whose workflow job changed since base; None when
    settings shared by every job (env, permissions) changed. Triggers
    (`on`, read by PyYAML as True) do not change what a job checks."""
    previous = subprocess.run(['git', 'show', f'{base}:{WORKFLOW}'], capture_output=True)
    if previous.returncode:
        return None
    old = yaml.safe_load(previous.stdout)
    new = yaml.safe_load(Path(WORKFLOW).read_text(encoding='utf-8'))
    old_jobs, new_jobs = old.pop('jobs'), new.pop('jobs')
    for workflow in (old, new):
        workflow.pop(True, None)
    if old != new:
        return None
    return {JOB_OUTPUTS[name] for name in JOB_OUTPUTS
            if new_jobs.get(name) != old_jobs.get(name)}


def scope(paths, base):
    result = {'lint': '', 'frontend': False, 'shell': False, 'release': False}
    whole = {tag: set() for tag in GO_TAGS}
    named = {tag: {} for tag in GO_TAGS}
    lint = set()
    go_all = lint_all = db_layer = critical_changed = False
    forced = set()
    go_files = []                                 # (path, changed in this diff)
    for name in paths:
        if name.startswith(FULL_PREFIXES) or name in FULL_FILES:
            return None
        if name == WORKFLOW:
            jobs = changed_jobs(base)
            if jobs is None:
                return None
            forced |= jobs
            continue
        if name in UNIT_FULL:
            forced.add('unit')
        if name == CRITICAL:
            critical_changed = True
        if name in NODE_SETUP:
            result['frontend'] = True
        if name.startswith('.gitea/'):
            continue
        if name.endswith('.md'):
            continue
        if name.startswith(DB_LAYER):
            db_layer = True
        if name.startswith('frontend/') or name == 'Makefile':
            result['frontend'] = True
        if (name.startswith('deploy/') or name == 'Dockerfile'
                or name.startswith('backend/resources/')):
            result['shell'] = True
        if name.startswith('.github/release-tools/'):
            result['release'] = True
        if name == 'backend/.golangci.yml':
            lint_all = True
        elif name.startswith('backend/'):
            go_files.append((Path(name), True))
        else:
            go_files.extend((path, False) for path in go_references(name))
    for path, changed in go_files:
        directory = package_dir(path)
        if directory is None or not path.parent.exists():
            # Outside any package (resources/ read at runtime) or a deleted package.
            go_all = True
            lint_all = lint_all or path.suffix == '.go'
            continue
        if changed and path.suffix == '.go':
            lint.add(package_arg(directory))
        for tag in GO_TAGS:
            select(whole[tag], named[tag], tag, directory, path)
    run_critical = {'unit': bool(go_files) or critical_changed,
                    'integration': db_layer or critical_changed}
    for path in critical_files():
        for tag in GO_TAGS:
            if run_critical[tag] and in_suite(path, tag):
                named[tag].setdefault(package_arg(path.parent), set()).update(test_names([path]))
    for tag in GO_TAGS:
        if go_all or tag in forced:
            result.update({tag: ALL, f'{tag}_selected': '', f'{tag}_run': ''})
        else:
            result.update(go_test_outputs(tag, whole[tag], named[tag]))
    result['lint'] = ALL if lint_all or 'lint' in forced else ' '.join(sorted(lint))
    for key in ('frontend', 'shell', 'release'):
        result[key] = result[key] or key in forced
    return result


def full():
    result = {'lint': ALL, 'frontend': True, 'shell': True, 'release': True}
    for tag in GO_TAGS:
        result.update({tag: ALL, f'{tag}_selected': '', f'{tag}_run': ''})
    return result


def shown(key, value):
    if isinstance(value, bool):
        return str(value).lower()
    if len(value) < 200:
        return value
    if key.endswith('_run'):
        return f'{value.count("|") + 1} tests'
    return f'{len(value.split())} packages'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--event', required=True)
    parser.add_argument('--base', default='')
    args = parser.parse_args()
    reason = 'scoped'
    if args.event != 'pull_request':
        result, reason = full(), f'event {args.event}'
    elif not re.fullmatch(r'[0-9a-f]{40}', args.base) or subprocess.run(
            ['git', 'cat-file', '-e', f'{args.base}^{{commit}}'], capture_output=True).returncode:
        result, reason = full(), 'missing base commit'
    else:
        paths = git_lines('diff', '--name-only', '--no-renames', '-z', f'{args.base}...HEAD', '--')
        result = scope(paths, args.base)
        if result is None:
            result, reason = full(), 'job image, module or shared workflow change'
    for key, value in result.items():
        print(f'{key}: {shown(key, value)}')
    print(f'reason: {reason}')
    output = os.environ.get('GITHUB_OUTPUT')
    if output:
        with open(output, 'a', encoding='utf-8') as handle:
            for key, value in result.items():
                handle.write(f'{key}={str(value).lower() if isinstance(value, bool) else value}\n')


if __name__ == '__main__':
    main()
