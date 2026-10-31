#!/usr/bin/env python3
"""Compile (never run) every package's race test binary in RUN --network=none.

Only invoke this reviewed helper in the dependency-prepared, nonroot build stage.
Its cache is image-build storage, not the runtime's memory-backed tmpfs.
"""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import runpy
import stat
import subprocess
import sys

spec = importlib.util.spec_from_file_location('isolated_runtime', Path(__file__).with_name('isolated-unit-entrypoint.py'))
runtime = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runtime)
require = runtime.require
plan_contract = runpy.run_path(str(Path(__file__).with_name('test-plan.py')))


def decode_packages(text):
    decoder, pos, result = json.JSONDecoder(), 0, []
    while pos < len(text):
        if text[pos].isspace():
            pos += 1
            continue
        value, pos = decoder.raw_decode(text, pos)
        require(isinstance(value, dict), 'non-object package inventory')
        result.append(value)
    require(result, 'empty package inventory')
    return result


def package_inventory(packages, source):
    result, seen = [], set()
    for package in packages:
        name = package.get('ImportPath', '')
        require(isinstance(name, str) and re.fullmatch(r'[A-Za-z0-9_./-]+', name) and name not in seen, 'invalid or duplicate package')
        require(not package.get('Error') and not package.get('DepsErrors') and not package.get('Incomplete'), 'package loading failed')
        directory = Path(package.get('Dir', ''))
        require(directory.is_absolute() and directory.is_dir() and not directory.is_symlink(), 'invalid package directory')
        relative = directory.relative_to(source).as_posix()
        require(relative == 'backend' or relative.startswith('backend/'), 'package outside backend')
        require(directory.resolve().is_relative_to((source / 'backend').resolve()), 'package directory escapes source')
        test_files = {}
        for field in ('TestGoFiles', 'XTestGoFiles'):
            values = package.get(field, [])
            require(isinstance(values, list) and len(values) == len(set(values)), 'invalid test file inventory')
            for namepart in values:
                require(isinstance(namepart, str) and Path(namepart).name == namepart and namepart.endswith('.go'), 'invalid test filename')
                require((directory / namepart).is_file() and not (directory / namepart).is_symlink(), 'missing test source')
            test_files[field] = sorted(values)
        seen.add(name)
        result.append({'package': name, 'directory': relative, **test_files})
    return sorted(result, key=lambda row: row['package'])


def compile_preflight(source, manifest, sha, output):
    require(source == Path('/src') and manifest == Path('/opt/isolation/source-manifest.json') and
            output == Path('/opt/isolation/compiled'), 'fixed build paths required')
    inherited = dict(os.environ)
    for key, build, normal in [('GOCACHE', '/tmp/build-cache', '/tmp/gocache'), ('GOTMPDIR', '/tmp/build-tmp', '/tmp/gotmp')]:
        if inherited.get(key) == build:
            inherited[key] = normal
    runtime.check_environment(inherited)
    require(os.getuid() != 0 and os.geteuid() != 0 and os.getgid() != 0 and os.getegid() != 0 and 0 not in os.getgroups(), 'nonroot compiler required')
    runtime.check_network([p.name for p in Path('/sys/class/net').iterdir()], Path('/proc/net/route').read_text(),
                          Path('/proc/net/ipv6_route').read_text() if Path('/proc/net/ipv6_route').exists() else '',
                          Path('/proc/net/if_inet6').read_text() if Path('/proc/net/if_inet6').exists() else '', runtime.loopback_address())
    runtime.check_socket_and_credentials(source)
    require(Path('/usr/local/go/VERSION').read_text().splitlines()[0] == runtime.GO_VERSION, 'pinned Go VERSION mismatch')
    require(Path('/opt/gomodcache').is_dir(), 'offline module cache missing')
    return runtime.verify_sources(source, manifest, sha)


def build(source, manifest, sha, output, suite, *, inspect=compile_preflight, execute=subprocess.run):
    require(suite in {'unit', 'integration'}, 'invalid suite')
    source_doc = inspect(source, manifest, sha, output)
    require(not output.is_symlink(), 'output symlink prohibited')
    if output.exists():
        require(output.is_dir() and not any(output.iterdir()), 'compile output must be empty')
    else:
        output.mkdir(parents=True)
    env = runtime.test_environment()
    env.update({'GOCACHE': '/tmp/build-cache', 'GOTMPDIR': '/tmp/build-tmp'})
    for directory in ('/tmp/build-cache', '/tmp/build-tmp', '/tmp/home', '/tmp/gopath'):
        Path(directory).mkdir(mode=0o700, exist_ok=True)
    version = execute([runtime.GO, 'version'], env=env, capture_output=True, text=True, check=False)
    require(version.returncode == 0 and re.fullmatch(r'go version go1\.27\.3 linux/(amd64|arm64)\n', version.stdout), 'pinned executable version mismatch')
    flags = ['-p', '1', '-race', '-tags=' + suite, '-mod=readonly']
    plan = source_doc.get('ci_plan')
    if plan is not None:
        plan_contract['validate'](plan, suite, source_doc['source_commit'])
    inventory_command = [runtime.GO, 'list', '-json', '-race', '-tags=' + suite, '-mod=readonly',
                         *plan_contract['package_args'](plan)]
    listed = execute(inventory_command,
                     cwd=source / 'backend', env=env, capture_output=True, text=True, check=False)
    if listed.returncode:
        print(listed.stderr, file=sys.stderr, flush=True)
    require(listed.returncode == 0, 'offline package inventory failed')
    inventory = package_inventory(decode_packages(listed.stdout), source)
    if plan is not None and plan['mode'] == 'fast':
        require({p['package'] for p in inventory} == set(plan['packages']), 'scoped package inventory mismatch')
    raw_inventory = (json.dumps(inventory, sort_keys=True, indent=2) + '\n').encode()
    (output / 'package-inventory.json').write_bytes(raw_inventory)
    binaries = output / 'binaries'
    binaries.mkdir()
    records = []
    for item in inventory:
        filename = runtime.digest(item['package'].encode()) + '.test'
        binary = binaries / filename
        command = [runtime.GO, 'test', '-c', *flags, '-o', str(binary), '.']
        result = execute(command, cwd=source / item['directory'], env=env, check=False)
        # Never execute a binary to list tests or infer whether TestMain exists.
        if result.returncode:
            print(json.dumps({'compile_failure_package': item['package'], 'exit_code': result.returncode}), file=sys.stderr, flush=True)
        require(result.returncode == 0, 'package test compilation failed: ' + item['package'])
        has_tests = bool(item['TestGoFiles'] or item['XTestGoFiles'])
        if has_tests:
            require(binary.is_file() and not binary.is_symlink(), 'expected compiled test binary missing')
            mode = stat.S_IMODE(binary.stat().st_mode)
            require(mode & 0o111 and not mode & 0o7022, 'unsafe compiled binary mode')
            info = {'binary': 'binaries/' + filename, 'binary_sha256': runtime.file_digest(binary),
                    'binary_mode': format(mode, '04o'), 'compile_status': 'compiled'}
        else:
            require(not binary.exists(), 'unexpected binary for no-test package')
            info = {'binary': None, 'compile_status': 'no_test_files'}
        records.append({**item, **info, 'compile_command': command})
    runtime.verify_sources(source, manifest, sha)
    receipt = {'schema': 1, 'suite': suite, 'source_commit': source_doc['source_commit'],
               'source_manifest_sha256': sha, 'source_receipt_sha256': source_doc['source_receipt_sha256'],
               'go_version': version.stdout.strip(), 'build_flags': flags, 'inventory_command': inventory_command,
               'network_mode': 'none_verified_loopback_only', 'test_binaries_executed': False,
               'package_inventory_sha256': runtime.digest(raw_inventory), 'package_count': len(records),
               'packages': records}
    (output / 'compile-receipt.json').write_text(json.dumps(receipt, sort_keys=True, indent=2) + '\n')
    print(json.dumps({'compile_only': True, 'suite': suite, 'package_count': len(records),
                      'compile_receipt_sha256': runtime.digest((output / 'compile-receipt.json').read_bytes())}), flush=True)
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source-root', type=Path, default=Path('/src'))
    parser.add_argument('--manifest', type=Path, default=Path('/opt/isolation/source-manifest.json'))
    parser.add_argument('--output', type=Path, default=Path('/opt/isolation/compiled'))
    parser.add_argument('--manifest-sha256', required=True)
    parser.add_argument('--suite', choices=['unit', 'integration'], required=True)
    args = parser.parse_args()
    try:
        return build(args.source_root, args.manifest, args.manifest_sha256, args.output, args.suite)
    except (runtime.IsolationError, OSError, ValueError, TypeError, KeyError) as error:
        print(json.dumps({'compile_only': False, 'error_type': type(error).__name__}), file=sys.stderr)
        return 125


if __name__ == '__main__':
    sys.exit(main())
