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
import resource
import shutil
import time
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
    public_input = 'frontend/src/constants/platformCatalog.builtin.json'
    require(public_input in files, 'platform catalog test input missing')
    require(all(name.startswith('backend/') or name in {'validation-receipt.json', public_input} for name in files), 'unreviewed non-backend source input')
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
    require(isinstance(tracked, dict) and 'backend/go.mod' in tracked and public_input in tracked, 'tracked source receipt required')
    for name, expected in tracked.items():
        if name.startswith('backend/') or name == public_input:
            require(files.get(name) == expected, 'tracked source receipt mismatch')
    for item in receipt.get('validation_support', []):
        destination, source = item.get('destination', ''), item.get('source', '')
        require((destination.startswith('backend/internal/isolatedfixture/') or
                 destination in {'backend/internal/securityaudit/isolated_shared_fixture_test.go',
                                 'backend/internal/repository/isolated_scoped_admin_unit_test.go',
                                 'backend/internal/service/isolated_scoped_admin_unit_test.go'}) and
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
    import runpy
    plan_contract = runpy.run_path(str(Path(__file__).with_name('test-plan.py')))
    plan = doc.get('ci_plan')
    if plan is not None:
        plan_contract['validate'](plan, suite, doc['source_commit'])
    require(receipt.get('inventory_command') == [GO, 'list', '-json', '-race', '-tags=' + suite, '-mod=readonly',
                                               *plan_contract['package_args'](plan)], 'package discovery flags differ from compilation')
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
    if plan is not None and plan['mode'] == 'fast':
        require(seen == set(plan['packages']), 'compiled packages differ from the source plan')
    return receipt, digest(raw)


def binary_command(record):
    return [GO, 'tool', 'test2json', '-p', record['package'], '-t',
            str(COMPILED_ROOT / record['binary']), *TEST_FLAGS, '-test.paniconexit0']


SERVICE_PACKAGE = 'github.com/Wei-Shaw/sub2api/internal/service'
FIXED_SERVICE_TESTS = (
    'TestGroupUsageDateUsesConfiguredTimezoneBoundary',
    'TestGroupUsageParseDateUsesConfiguredTimezone',
    'TestGroupUsageYesterdayStartHandlesDST',
    'TestSanitizeOpenAIResponsesToolParameterTypes_RewriteCountIndependentOfHits',
)
FIXED_SERVICE_TESTS_BY_SUITE = {'unit': FIXED_SERVICE_TESTS, 'integration': (FIXED_SERVICE_TESTS[-1],)}
SYNTHETIC_SERVICE_ENV = {'GEMINI_CLI_OAUTH_CLIENT_ID': 'custom-test-client'}


def package_environment(record, env):
    child = dict(env)
    if record['package'] == SERVICE_PACKAGE:
        # Public synthetic fixture set before package init snapshots OAuth ClientID.
        # Never inherit a client ID or credential from the host environment.
        child.update(SYNTHETIC_SERVICE_ENV)
    return child


def fixed_exclusion_regex(names):
    """RE2-compatible finite-name complement; size depends only on exclusions.

    The domain is the validated single-line top-level identifier registry.
    At each prefix accept an early end unless excluded, any differing next
    character plus its suffix, or recurse on a matching excluded prefix.
    """
    require(names and len(names) == len(set(names)), 'empty or duplicate partition names')
    tree = {}
    for name in names:
        require(name.isidentifier() and len(name) <= 512, 'invalid discovered top-level name')
        node = tree
        for char in name:
            node = node.setdefault(char, {})
        node[''] = None

    def encode(node):
        children = sorted(key for key in node if key)
        if not children:
            # This exact excluded name is forbidden, but every longer name is not.
            return '.+'
        alternatives = [''] if '' not in node else []
        alternatives.append('[^' + ''.join(re.escape(char) for char in children) + '].*')
        for char in children:
            alternatives.append(re.escape(char) + encode(node[char]))
        return '(' + '|'.join(alternatives) + ')'

    expression = '^' + encode(tree) + '$'
    require(len(expression.encode()) <= 65536, 'partition regex exceeds audited argument bound')
    return expression


def parse_test_registry(stdout):
    names, benchmarks, seen = [], [], set()
    for line in stdout.splitlines():
        if not line:
            continue
        # Registry listing is intentionally strict. Unexpected stdout is a red
        # inventory error, never silently discarded as if coverage were known.
        require(line.isidentifier() and len(line) <= 512, 'unexpected test registry output')
        require(line not in seen, 'duplicate discovered top-level test name')
        seen.add(line)
        if line.startswith('Benchmark'):
            benchmarks.append(line)
        else:
            require(line.startswith(('Test', 'Example', 'Fuzz')), 'unknown test registry name')
            names.append(line)
    return sorted(names), sorted(benchmarks)


def service_partitions(stdout, suite):
    require(suite in FIXED_SERVICE_TESTS_BY_SUITE, 'unknown partition suite')
    fixed = FIXED_SERVICE_TESTS_BY_SUITE[suite]
    names, benchmarks = parse_test_registry(stdout)
    require(all(name in names for name in fixed), 'fixed service test missing or renamed')
    if suite == 'integration':
        require(not set(FIXED_SERVICE_TESTS[:-1]).intersection(names), 'unit-only timezone test unexpectedly present in integration')
    names = sorted(names)
    main = [name for name in names if name not in fixed]
    require(main, 'main service test partition unexpectedly empty')
    partitions = [{'name': 'main', 'tests': main, 'regex': fixed_exclusion_regex(fixed)}]
    partitions.extend({'name': name, 'tests': [name], 'regex': '^' + re.escape(name) + '$'} for name in fixed)
    covered = []
    for partition in partitions:
        matched = [name for name in names if re.fullmatch(partition['regex'], name)]
        require(matched == sorted(partition['tests']), 'partition regex coverage mismatch')
        covered.extend(matched)
    require(sorted(covered) == names and len(covered) == len(set(covered)), 'partitions not exhaustive and disjoint')
    return {'discovered_tests_examples_fuzz': names, 'benchmarks_not_run_by_default': sorted(benchmarks),
            'partitions': partitions, 'coverage': 'exhaustive_disjoint_top_level; all matching subtests and fuzz seeds retained'}


def child_resource_snapshot(cgroup=Path('/sys/fs/cgroup')):
    """Read-only cgroup-v2 evidence; missing counters are unknown, never zero."""
    memory = {}
    for name in ('memory.events', 'memory.events.local', 'memory.current', 'memory.peak', 'memory.max'):
        try:
            raw = (cgroup / name).read_text().strip()
            if name.startswith('memory.events'):
                memory[name] = {key: int(value) for key, value in (line.split() for line in raw.splitlines())}
            else:
                memory[name] = int(raw) if raw.isdecimal() else raw
        except (OSError, ValueError):
            memory[name] = None
    usage = resource.getrusage(resource.RUSAGE_CHILDREN)
    return {'monotonic_seconds': time.monotonic(), 'cgroup_v2': memory,
            'reaped_children_cumulative': {key: getattr(usage, key) for key in
                ('ru_utime', 'ru_stime', 'ru_maxrss', 'ru_minflt', 'ru_majflt', 'ru_inblock', 'ru_oublock', 'ru_nvcsw', 'ru_nivcsw')}}


def cleanup_test_caches(paths=(Path('/tmp/gocache'), Path('/tmp/gotmp'))):
    """Reset only the two owned disposable caches, with no symlink traversal."""
    require(tuple(paths) == (Path('/tmp/gocache'), Path('/tmp/gotmp')), 'unexpected cache cleanup paths')
    require(shutil.rmtree.avoids_symlink_attacks, 'safe fd-based cache cleanup unavailable')
    result = []
    # Validate both roots before removing either. Never follow a substituted link.
    for root in paths:
        info = root.lstat()
        require(stat.S_ISDIR(info.st_mode) and info.st_uid == os.getuid(), 'cache root is not an owned directory')
    for root in paths:
        size = {'entries': 0, 'apparent_bytes': 0, 'symlinks': 0}
        for directory, dirs, files in os.walk(root, followlinks=False):
            for name in dirs + files:
                info = Path(directory, name).lstat()
                size['entries'] += 1
                size['apparent_bytes'] += info.st_size
                size['symlinks'] += int(stat.S_ISLNK(info.st_mode))
        shutil.rmtree(root)
        root.mkdir(mode=0o700)
        result.append({'path': str(root), 'before_cleanup': size, 'cleanup': 'removed_and_recreated_0700'})
    return result


def execute_suite(source, doc, env, expected_digest, suite, execute, fixture=None):
    if 'ci_plan' in doc:
        import runpy
        from types import SimpleNamespace
        planned = runpy.run_path(str(Path(__file__).with_name('planned-tests.py')))
        return planned['execute_suite'](SimpleNamespace(**globals()), source, doc, env,
                                        expected_digest, suite, execute, fixture)
    compiled, compiled_digest = verify_compiled(source, doc, expected_digest, suite)
    for name in ('home', 'gopath', 'gocache', 'gotmp'):
        Path('/tmp', name).mkdir(mode=0o700, exist_ok=True)
    service = [r for r in compiled['packages'] if r['package'] == SERVICE_PACKAGE and r['binary'] is not None]
    require(len(service) == 1, 'fixed service partition package missing or duplicate')
    receipt = {'isolation_preflight': 'passed', 'suite': suite,
               'source_commit': doc['source_commit'], 'manifest_sha256': expected_digest,
               'source_receipt_sha256': doc['source_receipt_sha256'], 'files': doc['files'],
               'environment_keys': sorted(env), 'test_execution': 'precompiled race binaries; fixed upfront per-suite service process partitions',
               'synthetic_service_child_environment': SYNTHETIC_SERVICE_ENV,
               'compile_receipt_sha256': compiled_digest, 'compiled_package_count': compiled['package_count'],
               'test_commands': [], 'fixed_service_tests': list(FIXED_SERVICE_TESTS_BY_SUITE[suite]),
               'no_test_file_packages': [r['package'] for r in compiled['packages'] if r['binary'] is None],
               'test_exit_code': None, 'runtime_observations': doc.get('runtime_observations', {}),
               'fixture_receipt': fixture, 'package_results': []}
    print(json.dumps({'record_type': 'isolation_receipt', **receipt}, sort_keys=True), flush=True)
    receipt['child_process_resources'] = []

    def observed_execute(label, command, runner, **kwargs):
        before = child_resource_snapshot()
        print(json.dumps({'record_type': 'child_resource_start', 'label': label, 'before': before}, sort_keys=True), flush=True)
        completed = None
        try:
            completed = runner(command, **kwargs)
            return completed
        finally:
            after = child_resource_snapshot()
            observation = {'label': label, 'before': before, 'after': after,
                           'usage_scope': 'RUSAGE_CHILDREN cumulative reaped children; maxrss is cumulative high-water, not a delta',
                           'raw_exit_code': getattr(completed, 'returncode', None),
                           'exit_status_scope': 'metadata binary for registry; go tool test2json for tests, underlying binary signal unknown'}
            observation['reaped_children_usage_delta'] = {
                key: after['reaped_children_cumulative'][key] - value
                for key, value in before['reaped_children_cumulative'].items() if key != 'ru_maxrss'}
            observation['cgroup_memory_events_delta'] = {
                key: after['cgroup_v2']['memory.events'][key] - value
                for key, value in (before['cgroup_v2']['memory.events'] or {}).items()
                if key in (after['cgroup_v2']['memory.events'] or {})}
            observation['cache_cleanup'] = 'failed'
            try:
                observation['caches'] = cleanup_test_caches()
                observation['cache_cleanup'] = 'completed'
            finally:
                observation['after_cache_cleanup'] = child_resource_snapshot()
                receipt['child_process_resources'].append(observation)
                print(json.dumps({'record_type': 'child_resource_end', **observation}, sort_keys=True), flush=True)

    audit = None
    if service:
        record = service[0]
        discovery_command = [str(COMPILED_ROOT / record['binary']), '-test.list=.', '-test.timeout=20m']
        # The listing invokes package init/TestMain only within the already
        # verified network-none runtime. It is not a test execution or retry.
        listed = observed_execute('service_registry_all', discovery_command, execute, cwd=source / record['directory'],
                         env=package_environment(record, env), capture_output=True, text=True,
                         check=False, timeout=1200)
        print(json.dumps({'record_type': 'service_registry_listing', 'package': SERVICE_PACKAGE,
                          'binary_sha256': record['binary_sha256'], 'exit_code': listed.returncode,
                          'stdout': listed.stdout, 'stderr': listed.stderr}, sort_keys=True), flush=True)
        require(listed.returncode == 0, 'service test inventory process failed')
        audit = service_partitions(listed.stdout, suite)
        audit.update({'package': SERVICE_PACKAGE, 'binary_sha256': record['binary_sha256'],
                      'discovery_command': discovery_command, 'discovery_exit_code': listed.returncode,
                      'discovery_stderr': listed.stderr, 'strategy': 'fixed_before_any_test_outcome_no_retries'})
        # Independently exercise Go's own regexp engine before any tests run.
        # This fixed second metadata process is not a failure-triggered retry.
        main_listing_command = [str(COMPILED_ROOT / record['binary']),
                                '-test.list=' + audit['partitions'][0]['regex'], '-test.timeout=20m']
        selected = observed_execute('service_registry_main', main_listing_command, execute, cwd=source / record['directory'],
                           env=package_environment(record, env), capture_output=True, text=True,
                           check=False, timeout=1200)
        verification = {'command': main_listing_command, 'exit_code': selected.returncode,
                        'stdout': selected.stdout, 'stderr': selected.stderr, 'status': 'failed'}
        audit['go_main_regex_verification'] = verification
        try:
            require(selected.returncode == 0, 'Go main regex inventory process failed')
            selected_names, selected_benchmarks = parse_test_registry(selected.stdout)
            verification.update({'tests_examples_fuzz': selected_names, 'benchmarks': selected_benchmarks})
            require(selected_names == sorted(audit['partitions'][0]['tests']), 'Go main regex registry mismatch')
            require(selected_benchmarks == audit['benchmarks_not_run_by_default'], 'Go main regex benchmark registry mismatch')
            verification['status'] = 'passed'
        finally:
            print(json.dumps({'record_type': 'go_main_regex_verification', **verification}, sort_keys=True), flush=True)
        receipt['service_partition_audit'] = audit
        print(json.dumps({'record_type': 'service_partition_audit', **audit}, sort_keys=True), flush=True)
    plans = []
    for record in compiled['packages']:
        if record['binary'] is None:
            continue
        if record['package'] == SERVICE_PACKAGE:
            partitions = audit['partitions']
        else:
            partitions = [{'name': 'all', 'regex': None}]
        for partition in partitions:
            command = binary_command(record)
            if partition['regex'] is not None:
                command = [*command, '-test.run=' + partition['regex']]
            plans.append((record, partition['name'], command))
    receipt['test_commands'] = [command for _, _, command in plans]
    failed = False
    for record in compiled['packages']:
        if record['binary'] is None:
            receipt['package_results'].append({'package': record['package'], 'status': 'no_test_files', 'compiled': True})
    # The complete immutable schedule exists before the first test starts. Every
    # scheduled process runs once even when any previous process fails.
    for record, partition_name, command in plans:
        completed = observed_execute(record['package'] + ':' + partition_name, command, execute, cwd=source / record['directory'], env=package_environment(record, env), check=False,
                            stdout=sys.stdout, stderr=subprocess.STDOUT)
        code = completed.returncode if completed.returncode >= 0 else 128 - completed.returncode
        failed = failed or code != 0
        receipt['package_results'].append({'package': record['package'], 'partition': partition_name,
                                           'raw_exit_code': completed.returncode, 'exit_code': code,
                                           'binary_sha256': record['binary_sha256']})
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
    except (IsolationError, OSError, ValueError, TypeError, subprocess.TimeoutExpired) as exc:
        # Never print offending environment values, file contents or credentials.
        print(json.dumps({'isolation_preflight': 'failed', 'error_type': type(exc).__name__, 'check_failure': str(exc) if isinstance(exc, IsolationError) else 'system preflight failure'}), file=sys.stderr)
        return 125


if __name__ == '__main__':
    sys.exit(main())
