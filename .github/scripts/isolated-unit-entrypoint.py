#!/usr/bin/env python3
"""Fail-closed preflight for a host-verified Docker --network none unit runner.

This is defense in depth, not a substitute for Docker inspect or image provenance.
No subprocess (including `go version`) runs before all pure preflight checks pass.
"""
import argparse
import hashlib
import importlib.util
import fcntl
import ipaddress
import json
import os
from pathlib import Path, PurePosixPath
import re
import stat
import socket
import struct
import subprocess
import sys

GO = '/usr/local/go/bin/go'
GO_VERSION = 'go1.27.3'
TEST_FLAGS = ['-test.v=test2json', '-test.timeout=20m', '-test.count=1']
COMPILED_ROOT = Path('/opt/isolation/compiled')


class IsolationError(Exception):
    pass


def require(condition, message):
    if not condition:
        raise IsolationError(message)


def digest(data):
    return hashlib.sha256(data).hexdigest()


def file_digest(path):
    checksum = hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            checksum.update(block)
    return checksum.hexdigest()


def check_environment(env):
    exact = {
        'PATH': '/usr/local/go/bin:/usr/bin:/bin', 'HOME': '/tmp/home',
        'TMPDIR': '/tmp', 'GOCACHE': '/tmp/gocache', 'GOTMPDIR': '/tmp/gotmp',
        'GOMODCACHE': '/opt/gomodcache', 'GOPATH': '/tmp/gopath',
        'GOPROXY': 'off', 'GOSUMDB': 'off', 'GOTOOLCHAIN': 'local',
        'GOMAXPROCS': '2', 'GOGC': '30', 'GOMEMLIMIT': '3GiB', 'CI': 'true',
        # Python may coerce the locale even under env -i; accept only this value.
        'LC_CTYPE': 'C.UTF-8',
    }
    for key, value in env.items():
        require(key in exact, 'unexpected inherited environment key')
        require(value == exact[key], 'unexpected inherited environment value')


def test_environment():
    # Never copy os.environ; no proxy, token, cloud credentials, GOFLAGS or GOENV.
    return {
        'PATH': '/usr/local/go/bin:/usr/bin:/bin', 'HOME': '/tmp/home',
        'LANG': 'C.UTF-8', 'LC_ALL': 'C.UTF-8', 'TZ': 'UTC',
        'GOPROXY': 'off', 'GOSUMDB': 'off', 'GOTOOLCHAIN': 'local', 'GOENV': 'off',
        'GOPATH': '/tmp/gopath', 'GOMODCACHE': '/opt/gomodcache',
        'GOCACHE': '/tmp/gocache', 'GOTMPDIR': '/tmp/gotmp', 'TMPDIR': '/tmp',
        'CGO_ENABLED': '1', 'GOMAXPROCS': '2', 'GOGC': '30', 'GOMEMLIMIT': '3GiB', 'CI': 'true',
    }


def check_status(text):
    fields = dict(line.split(':', 1) for line in text.splitlines() if ':' in line)
    for key in ('Uid', 'Gid'):
        values = fields.get(key, '').split()
        require(len(values) == 4 and all(v.isdigit() and int(v) != 0 for v in values), 'root identity prohibited')
    require('Groups' in fields, 'missing supplementary group information')
    groups = fields['Groups'].split()
    require(all(g.isdigit() and int(g) != 0 for g in groups), 'root supplementary group prohibited')
    for key in ('CapInh', 'CapPrm', 'CapEff', 'CapBnd', 'CapAmb'):
        require(re.fullmatch(r'0+', fields.get(key, '').strip()), 'capability set must be empty')
    require(fields.get('NoNewPrivs', '').strip() == '1', 'NoNewPrivs must be enabled')


def check_network(interfaces, ipv4_routes, ipv6_routes, ipv6_addresses, ipv4_loopback):
    require(ipv4_loopback == "127.0.0.1", "IPv4 loopback address required")
    require(set(interfaces) == {'lo'}, 'only loopback interface permitted')
    lines = ipv4_routes.splitlines()
    require(lines and lines[0].split()[:2] == ['Iface', 'Destination'], 'missing IPv4 route header')
    for line in lines[1:]:
        f = line.split()
        require(len(f) >= 8 and f[0] == 'lo', 'non-loopback IPv4 route')
        try:
            addr = ipaddress.IPv4Address(int.from_bytes(bytes.fromhex(f[1]), 'little'))
            mask = int.from_bytes(bytes.fromhex(f[7]), 'little')
            gateway = int(f[2], 16)
        except (ValueError, OverflowError):
            raise IsolationError('malformed IPv4 route') from None
        require(addr.is_loopback and mask >= 0xff000000 and gateway == 0, 'non-loopback IPv4 destination')
    for line in ipv6_addresses.splitlines():
        f = line.split()
        require(len(f) == 6 and f[0] == '00000000000000000000000000000001' and f[5] == 'lo', 'non-loopback IPv6 address')
    for line in ipv6_routes.splitlines():
        f = line.split()
        require(len(f) == 10 and f[9] == 'lo', 'non-loopback IPv6 route')
        try:
            addr = ipaddress.IPv6Address(int(f[0], 16))
            prefix, flags = int(f[1], 16), int(f[8], 16)
            gateway = int(f[4], 16)
        except ValueError:
            raise IsolationError('malformed IPv6 route') from None
        # Linux can publish unreachable ::/0 entries on lo. RTF_REJECT prevents
        # forwarding; only that exact kernel reject case, never a default route.
        reject_default = int(addr) == 0 and prefix == 0 and flags & 0x200
        require(gateway == 0 and ((addr.is_loopback and prefix == 128) or reject_default), 'forwardable non-loopback IPv6 route')


def check_mounts(text):
    allowed = {'/', '/proc', '/dev', '/dev/pts', '/sys', '/sys/fs/cgroup', '/dev/mqueue', '/dev/shm', '/tmp',
               '/etc/resolv.conf', '/etc/hostname', '/etc/hosts', '/proc/bus', '/proc/fs', '/proc/irq',
               '/proc/sys', '/proc/sysrq-trigger', '/proc/acpi', '/proc/interrupts', '/proc/kcore',
               '/proc/keys', '/proc/latency_stats', '/proc/timer_list', '/proc/timer_stats', '/proc/asound', '/proc/sched_debug', '/proc/scsi', '/sys/firmware', '/sys/devices/virtual/powercap'}
    mounts = {}
    for line in text.splitlines():
        fields = line.split()
        require(len(fields) >= 10 and '-' in fields, 'malformed mount table')
        mountpoint = fields[4]
        require(mountpoint in allowed, 'unexpected mount (host bind or credentials prohibited)')
        require(mountpoint not in mounts, 'duplicate mountpoint')
        mounts[mountpoint] = (set(fields[5].split(',')), fields[fields.index('-') + 1])
    require('/' in mounts and 'ro' in mounts['/'][0], 'root filesystem must be read-only')
    require('/tmp' in mounts and mounts['/tmp'][1] == 'tmpfs', 'private tmpfs /tmp required')
    require({'nosuid', 'nodev'} <= mounts['/tmp'][0] and 'noexec' not in mounts['/tmp'][0], 'invalid /tmp safety or execution flags')


def check_socket_and_credentials(source):
    for base in (source, Path('/run'), Path('/tmp'), Path('/home')):
        if not base.exists():
            continue
        for root, dirs, files in os.walk(base, followlinks=False, onerror=lambda _: (_ for _ in ()).throw(IsolationError('unreadable runtime path'))):
            for name in dirs + files:
                path = Path(root) / name
                require(not stat.S_ISSOCK(path.lstat().st_mode), 'runtime socket prohibited')
                require(name not in {'.aws', '.azure', 'gcloud', '.kube', '.ssh', '.netrc', '.docker', '.env'}, 'credential file or directory prohibited')
    for path in ('/var/run/docker.sock', '/run/docker.sock', '/run/containerd/containerd.sock', '/run/secrets', '/var/run/secrets'):
        require(not os.path.lexists(path), 'host socket or injected secret path prohibited')


def verify_sources(root, manifest_path, expected_digest):
    require(not root.is_symlink() and root.is_dir(), 'source root must be a directory')
    require(re.fullmatch(r'[0-9a-f]{64}', expected_digest), 'expected manifest digest required')
    require(not manifest_path.is_symlink(), 'manifest symlink prohibited')
    raw = manifest_path.read_bytes()
    require(digest(raw) == expected_digest, 'source manifest digest mismatch')
    doc = json.loads(raw)
    require(doc.get('schema') == 1 and re.fullmatch(r'[0-9a-f]{40}', doc.get('source_commit', '')), 'invalid manifest metadata')
    files = doc.get('files')
    require(isinstance(files, dict) and files and 'validation-receipt.json' in files and 'backend/go.mod' in files, 'source manifest incomplete')
    actual = set()
    for path in root.rglob('*'):
        require(not path.is_symlink(), 'source symlink prohibited')
        if path.is_dir():
            continue
        require(path.is_file(), 'source special file prohibited')
        actual.add(path.relative_to(root).as_posix())
    require(actual == set(files), 'source file inventory mismatch')
    require(all(name.startswith('backend/') or name == 'validation-receipt.json' for name in files), 'only backend and receipt source inputs permitted')
    for name, expected in files.items():
        pure = PurePosixPath(name)
        require(not pure.is_absolute() and '..' not in pure.parts and str(pure) == name, 'unsafe manifest path')
        require(re.fullmatch(r'[0-9a-f]{64}', expected), 'invalid file digest')
        require(digest((root / name).read_bytes()) == expected, 'source checksum mismatch')
    receipt_bytes = (root / 'validation-receipt.json').read_bytes()
    require(digest(receipt_bytes) == doc.get('source_receipt_sha256'), 'validation receipt digest mismatch')
    receipt = json.loads(receipt_bytes)
    require(receipt.get('tested_commit') == doc['source_commit'] and not receipt.get('working_tree_diff'), 'source receipt commit or cleanliness mismatch')
    tracked = receipt.get('source_sha256')
    require(isinstance(tracked, dict) and 'backend/go.mod' in tracked, 'tracked backend receipt required')
    for name, expected in tracked.items():
        if name.startswith('backend/'):
            require(files.get(name) == expected, 'tracked backend source receipt mismatch')
    for item in receipt.get('validation_support', []):
        destination, source = item.get('destination', ''), item.get('source', '')
        require((destination.startswith('backend/internal/isolatedfixture/') or
                 destination == 'backend/internal/securityaudit/isolated_shared_fixture_test.go') and
                source.startswith('.github/upgrade/fixtures/') and
                files.get(destination) == item.get('sha256') == tracked.get(source),
                'validation-only support receipt mismatch')
    require(re.search(r'^go 1\.27\.3$', (root / 'backend/go.mod').read_text(), re.M), 'source Go version mismatch')
    return doc


def loopback_address():
    # ioctl reads interface configuration; it sends no packet or DNS request.
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
        address = fcntl.ioctl(sock.fileno(), 0x8915, struct.pack('256s', b'lo'))
    return socket.inet_ntoa(address[20:24])


def preflight(source, manifest, expected_digest):
    require(source == Path('/src') and manifest == Path('/opt/isolation/source-manifest.json'), 'fixed runtime paths required')
    check_environment(dict(os.environ))
    check_status(Path('/proc/self/status').read_text())
    check_network([p.name for p in Path('/sys/class/net').iterdir()], Path('/proc/net/route').read_text(),
                  Path('/proc/net/ipv6_route').read_text() if Path('/proc/net/ipv6_route').exists() else '',
                  Path('/proc/net/if_inet6').read_text() if Path('/proc/net/if_inet6').exists() else '', loopback_address())
    check_mounts(Path('/proc/self/mountinfo').read_text())
    check_socket_and_credentials(source)
    require(Path('/usr/local/go/VERSION').read_text().splitlines()[0] == GO_VERSION, 'pinned Go VERSION mismatch')
    require(Path('/opt/gomodcache').is_dir(), 'offline module cache missing')
    doc = verify_sources(source, manifest, expected_digest)
    doc['runtime_observations'] = {
        'interfaces': sorted(p.name for p in Path('/sys/class/net').iterdir()),
        'ipv4_routes': Path('/proc/net/route').read_text(),
        'ipv6_routes': Path('/proc/net/ipv6_route').read_text() if Path('/proc/net/ipv6_route').exists() else '',
        'effective_uid': os.geteuid(), 'effective_gid': os.getegid(),
        'no_new_privileges': True, 'all_capability_sets_empty': True,
        'root_read_only': True, 'writable_application_workspace': 'private /tmp tmpfs; standard isolated /dev and /dev/shm also exist',
    }
    return doc


def verify_compiled(source, doc, expected_digest, suite, compiled=COMPILED_ROOT):
    require(not compiled.is_symlink() and compiled.is_dir(), 'compiled directory missing or symlink')
    receipt_path = compiled / 'compile-receipt.json'
    inventory_path = compiled / 'package-inventory.json'
    require(not receipt_path.is_symlink() and not inventory_path.is_symlink(), 'compiled manifest symlink prohibited')
    raw = receipt_path.read_bytes()
    receipt = json.loads(raw)
    require(receipt.get('schema') == 1 and receipt.get('suite') == suite, 'compiled suite mismatch')
    require(receipt.get('source_commit') == doc['source_commit'] and
            receipt.get('source_manifest_sha256') == expected_digest and
            receipt.get('source_receipt_sha256') == doc['source_receipt_sha256'], 'compiled source provenance mismatch')
    require(re.fullmatch(r'go version go1\.27\.3 linux/(amd64|arm64)', receipt.get('go_version', '')), 'compiled toolchain mismatch')
    flags = ['-p', '1', '-race', '-tags=' + suite, '-mod=readonly']
    require(receipt.get('build_flags') == flags and receipt.get('test_binaries_executed') is False and
            receipt.get('network_mode') == 'none_verified_loopback_only', 'compiled isolation or flags mismatch')
    require(receipt.get('inventory_command') == [GO, 'list', '-json', '-race', '-tags=' + suite, '-mod=readonly', './...'], 'package discovery flags differ from compilation')
    raw_inventory = inventory_path.read_bytes()
    require(digest(raw_inventory) == receipt.get('package_inventory_sha256'), 'package inventory checksum mismatch')
    inventory, records = json.loads(raw_inventory), receipt.get('packages')
    require(isinstance(inventory, list) and inventory and isinstance(records, list) and
            len(records) == len(inventory) == receipt.get('package_count'), 'incomplete compiled package inventory')
    seen, expected_files = set(), {'compile-receipt.json', 'package-inventory.json'}
    for item, record in zip(inventory, records):
        require(isinstance(item, dict) and isinstance(record, dict), 'invalid package record')
        require(all(record.get(k) == item.get(k) for k in ('package', 'directory', 'TestGoFiles', 'XTestGoFiles')), 'compiled package inventory differs')
        name, relative = item.get('package', ''), item.get('directory', '')
        require(isinstance(name, str) and re.fullmatch(r'[A-Za-z0-9_./-]+', name) and name not in seen, 'invalid or duplicate compiled package')
        seen.add(name)
        pure = PurePosixPath(relative)
        require(not pure.is_absolute() and '..' not in pure.parts and str(pure) == relative and
                (relative == 'backend' or relative.startswith('backend/')), 'invalid package source directory')
        directory = source / relative
        require(directory.is_dir() and not directory.is_symlink() and directory.resolve().is_relative_to((source / 'backend').resolve()), 'package source directory missing or escaping')
        for field in ('TestGoFiles', 'XTestGoFiles'):
            values = item.get(field)
            require(isinstance(values, list) and len(values) == len(set(values)), 'invalid compiled test file inventory')
            for filename in values:
                require(isinstance(filename, str) and Path(filename).name == filename and filename.endswith('.go') and
                        (directory / filename).is_file() and not (directory / filename).is_symlink(), 'missing compiled test source')
        filename = 'binaries/' + digest(name.encode()) + '.test'
        expected_command = [GO, 'test', '-c', *flags, '-o', str(COMPILED_ROOT / filename), '.']
        require(record.get('compile_command') == expected_command, 'unexpected compile command')
        has_tests = bool(item['TestGoFiles'] or item['XTestGoFiles'])
        if not has_tests:
            require(record.get('binary') is None and record.get('compile_status') == 'no_test_files', 'no-test package metadata mismatch')
            continue
        require(record.get('compile_status') == 'compiled' and record.get('binary') == filename, 'missing compiled binary record')
        binary = compiled / filename
        require(binary.is_file() and not binary.is_symlink(), 'compiled binary missing or symlink')
        mode = stat.S_IMODE(binary.stat().st_mode)
        require(mode & 0o111 and not mode & 0o7022 and format(mode, '04o') == record.get('binary_mode'), 'compiled binary permissions mismatch')
        require(file_digest(binary) == record.get('binary_sha256'), 'compiled binary checksum mismatch')
        expected_files.add(filename)
    actual = set()
    for path in compiled.rglob('*'):
        require(not path.is_symlink(), 'compiled directory contains symlink')
        if path.is_dir():
            continue
        require(path.is_file(), 'compiled directory contains special file')
        actual.add(path.relative_to(compiled).as_posix())
    require(actual == expected_files, 'compiled file inventory mismatch')
    return receipt, digest(raw)


def binary_command(record):
    return [GO, 'tool', 'test2json', '-p', record['package'], '-t',
            str(COMPILED_ROOT / record['binary']), *TEST_FLAGS]


def execute_suite(source, doc, env, expected_digest, suite, execute, fixture=None):
    # Verify every binary and every package before running the first test.
    compiled, compiled_digest = verify_compiled(source, doc, expected_digest, suite)
    for name in ('home', 'gopath', 'gocache', 'gotmp'):
        Path('/tmp', name).mkdir(mode=0o700, exist_ok=True)
    receipt = {'isolation_preflight': 'passed', 'suite': suite,
               'source_commit': doc['source_commit'], 'manifest_sha256': expected_digest,
               'source_receipt_sha256': doc['source_receipt_sha256'], 'files': doc['files'],
               'environment_keys': sorted(env), 'test_execution': 'all precompiled race binaries, sequential test2json',
               'compile_receipt_sha256': compiled_digest, 'compiled_package_count': compiled['package_count'],
               'test_commands': [binary_command(r) for r in compiled['packages'] if r['binary'] is not None],
               'no_test_file_packages': [r['package'] for r in compiled['packages'] if r['binary'] is None],
               'test_exit_code': None, 'runtime_observations': doc.get('runtime_observations', {}),
               'fixture_receipt': fixture, 'package_results': []}
    print(json.dumps({'record_type': 'isolation_receipt', **receipt}, sort_keys=True), flush=True)
    failed = False
    for record in compiled['packages']:
        if record['binary'] is None:
            # Compile-only success is recorded, not invented test execution.
            receipt['package_results'].append({'package': record['package'], 'status': 'no_test_files', 'compiled': True})
            continue
        completed = execute(binary_command(record), cwd=source / record['directory'], env=env, check=False,
                            stdout=sys.stdout, stderr=subprocess.STDOUT)
        code = completed.returncode if completed.returncode >= 0 else 128 - completed.returncode
        failed = failed or code != 0
        receipt['package_results'].append({'package': record['package'], 'raw_exit_code': completed.returncode,
                                           'exit_code': code, 'binary_sha256': record['binary_sha256']})
    receipt['test_exit_code'] = 1 if failed else 0
    return receipt


def run(source, manifest, expected_digest, *, suite='unit', inspect=preflight, execute=subprocess.run):
    require(suite in {'unit', 'integration'}, 'unknown test suite')
    if suite == 'integration':
        spec = importlib.util.spec_from_file_location('integration_fixtures',
                    Path(__file__).with_name('integration-bootstrap.py'))
        fixtures = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(fixtures)
        # This helper performs the complete shared pure preflight before even
        # version probes or fixture initialization. All binaries remain offline.
        def tests(doc, env, fixture):
            return execute_suite(source, doc, env, expected_digest, suite, execute, fixture)
        receipt, fixture = fixtures.run_with_postgres(source, manifest, expected_digest, tests)
        receipt['fixture_receipt'] = fixture  # Terminal evidence includes cleanup.
    else:
        doc = inspect(source, manifest, expected_digest)
        env = test_environment()
        version = execute([GO, 'version'], env=env, capture_output=True, text=True, check=False)
        require(version.returncode == 0 and re.fullmatch(r'go version go1\.27\.3 linux/(amd64|arm64)\n', version.stdout), 'pinned executable Go version mismatch')
        receipt = execute_suite(source, doc, env, expected_digest, suite, execute)
    print(json.dumps({'record_type': 'isolation_receipt', **receipt}, sort_keys=True), flush=True)
    return receipt['test_exit_code']


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source-root', type=Path, default=Path('/src'))
    parser.add_argument('--manifest', type=Path, default=Path('/opt/isolation/source-manifest.json'))
    parser.add_argument('--manifest-sha256', required=True)
    parser.add_argument('--suite', choices=['unit', 'integration'], default='unit')
    args = parser.parse_args()
    try:
        return run(args.source_root, args.manifest, args.manifest_sha256, suite=args.suite)
    except (IsolationError, OSError, ValueError, TypeError) as exc:
        # Never print offending environment values, file contents or credentials.
        print(json.dumps({'isolation_preflight': 'failed', 'error_type': type(exc).__name__, 'check_failure': str(exc) if isinstance(exc, IsolationError) else 'system preflight failure'}), file=sys.stderr)
        return 125


if __name__ == '__main__':
    sys.exit(main())
