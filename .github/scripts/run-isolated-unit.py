#!/usr/bin/env python3
"""Prepare pinned dependencies, then run unit tests only inside network=none.

No test code executes in the networked preparation stage. No host paths or
credentials are mounted/passed to the runtime. The output directory is new,
private and kept for investigation. Failures never fall back to host tests.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
GO_VERSION = '1.27.3'
BASE_IMAGE = f'golang:{GO_VERSION}-bookworm'


def run(args, **kwargs):
    return subprocess.run(args, check=True, text=True, **kwargs)


def output(args):
    return subprocess.check_output(args, text=True).strip()


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def assert_container(data, image_id, command):
    """Inspect the *created* container before any entrypoint can execute."""
    h, c = data['HostConfig'], data['Config']
    checks = {
        'image': data['Image'] == image_id,
        'network': h.get('NetworkMode') == 'none',
        'nonroot': c.get('User') == '65532:65532',
        'privileged': h.get('Privileged') is False,
        'readonly': h.get('ReadonlyRootfs') is True,
        'caps': h.get('CapDrop') == ['ALL'] and not h.get('CapAdd'),
        'no_new_privileges': 'no-new-privileges' in h.get('SecurityOpt', []),
        'namespaces': h.get('PidMode', '') == '' and h.get('IpcMode') == 'private'
                      and h.get('UTSMode', '') == '' and h.get('CgroupnsMode') == 'private',
        'no_host_access': not any(h.get(k) for k in
                                 ['Binds', 'Devices', 'DeviceRequests', 'Links',
                                  'ExtraHosts', 'PortBindings', 'Sysctls']),
        'no_publish': not h.get('PublishAllPorts'),
        'no_image_volumes': not c.get('Volumes'),
        'tmpfs_only': set(h.get('Tmpfs', {})) == {'/tmp'},
        'mounts': all(m.get('Type') == 'tmpfs' and m.get('Destination') == '/tmp'
                      for m in data.get('Mounts', [])),
        'command': data.get('Path') == command[0] and data.get('Args') == command[1:],
    }
    bad = [key for key, ok in checks.items() if not ok]
    if bad:
        raise RuntimeError('container isolation preflight failed: '+', '.join(bad))
    return checks


def collect_stream(work, expected_commit, expected_manifest, suite='unit'):
    """Preserve streamed receipts even after tmpfs/container teardown."""
    receipts, counts, skipped = [], {'pass': 0, 'fail': 0, 'skip': 0}, []
    with (work / 'runtime-output.jsonl').open() as stream, (work / (suite+'-tests.jsonl')).open('w') as tests:
        for line in stream:
            try:
                event = json.loads(line)
            except ValueError:
                tests.write(line)
                continue
            if event.get('record_type') == 'isolation_receipt':
                receipts.append(event)
            else:
                tests.write(line)
                if event.get('Test') and event.get('Action') in counts:
                    counts[event['Action']] += 1
                    if event['Action'] == 'skip':
                        skipped.append({'package': event.get('Package'), 'test': event['Test']})
    receipt = receipts[-1] if receipts else {'isolation_preflight': 'not_proven'}
    receipt['test_event_counts'] = counts
    receipt['skipped_tests'] = skipped
    receipt['streamed_receipt_count'] = len(receipts)
    (work / 'isolation-receipt.json').write_text(json.dumps(receipt, indent=2)+'\n')
    if (not receipts or any(r.get('source_commit') != expected_commit or
                             r.get('manifest_sha256') != expected_manifest or
                             r.get('isolation_preflight') != 'passed' or r.get('suite') != suite for r in receipts) or
            receipts[0].get('test_exit_code') is not None or
            receipt.get('isolation_preflight') != 'passed' or
            receipt.get('source_commit') != expected_commit or
            receipt.get('manifest_sha256') != expected_manifest or
            receipt.get('suite') != suite or
            len(receipts) != 2 or receipt.get('test_exit_code') is None):
        raise RuntimeError('incomplete or mismatched runtime isolation evidence')
    if suite == 'integration' and (receipt.get('fixture_receipt', {}).get('cleanup') not in {'stopped', 'stopped-immediate'} or
                                   not receipt.get('fixture_receipt', {}).get('postgres_ready')):
        raise RuntimeError('integration fixtures were not ready or cleanly stopped')
    if receipt['test_exit_code'] == 0 and not counts['pass']:
        raise RuntimeError('no executed test passes; refusing an empty green suite')
    print(json.dumps({'suite': suite, 'test_events': counts, 'inherited_skips': skipped}, sort_keys=True))
    return receipt


def prepare_context(work, suite='unit'):
    if output(['git', '-C', str(ROOT), 'status', '--porcelain', '--untracked-files=no']):
        raise RuntimeError('commit the exact tracked source before preparing isolation')
    overlay, context = work / 'overlay', work / 'context'
    run([sys.executable, str(ROOT / '.github/scripts/prepare-upgrade-validation.py'), str(overlay)])
    receipt = json.loads((overlay / 'validation-receipt.json').read_text())
    if receipt['working_tree_diff']:
        raise RuntimeError('validation source is dirty')
    allowed = set(receipt['source_sha256']) | {x['path'] for x in receipt['restored']}
    support_specs = json.loads((ROOT / '.github/upgrade/manifest.json').read_text()).get('validation_support', {})
    support = receipt.get('validation_support', [])
    if {x['destination'] for x in support} != set(support_specs) or len(support) != len(support_specs):
        raise RuntimeError('validation support inventory differs from reviewed manifest')
    for item in support:
        dest, source = item['destination'], item['source']
        expected = support_specs[dest]
        if (not dest.startswith('backend/internal/isolatedfixture/') or
                item['sha256'] != expected['sha256'] or source != expected['source'] or
                receipt['source_sha256'].get(source) != item['sha256'] or
                sha(overlay / dest) != item['sha256']):
            raise RuntimeError('unreviewed isolated support source')
        allowed.add(dest)
    backend = {p for p in allowed if p.startswith('backend/')}
    actual = {str(p.relative_to(overlay)) for p in (overlay / 'backend').rglob('*') if p.is_file()}
    if actual != backend:
        raise RuntimeError('backend contains unreceipted or missing files')
    context.mkdir()
    files = {}
    for path in sorted(backend | {'validation-receipt.json'}):
        source = overlay / path
        if source.is_symlink() or not source.is_file():
            raise RuntimeError('nonregular source rejected: '+path)
        if any(part == '.git' or part == '.env' or part.startswith('.env.') for part in Path(path).parts):
            raise RuntimeError('environment/credential files excluded from image: '+path)
        dest = context / path
        dest.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, dest)
        files[path] = sha(dest)
    manifest = {'schema': 1, 'source_commit': receipt['tested_commit'],
                'source_receipt_sha256': sha(context / 'validation-receipt.json'), 'files': files}
    (context / 'source-manifest.json').write_text(json.dumps(manifest, sort_keys=True, indent=2)+'\n')
    shutil.copy2(ROOT / ('.github/isolation/'+suite+'.Dockerfile'), context / 'Dockerfile')
    shutil.copy2(ROOT / '.github/scripts/isolated-unit-entrypoint.py', context / 'isolated-unit-entrypoint.py')
    if suite == 'integration':
        shutil.copy2(ROOT / '.github/scripts/integration-bootstrap.py', context / 'integration-bootstrap.py')
    return context, manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('output_directory', type=Path)
    parser.add_argument('--suite', choices=['unit', 'integration'], default='unit')
    args = parser.parse_args()
    work = args.output_directory.resolve()
    if work.exists() or work == ROOT or ROOT in work.parents:
        parser.error('use a fresh output directory outside the repository')
    work.mkdir(mode=0o700, parents=True)
    context, source = prepare_context(work, args.suite)
    run(['docker', 'info'], stdout=subprocess.DEVNULL)
    # Only public toolchain/modules/OS dependencies are fetched, never tests.
    images = {'GO_IMAGE': BASE_IMAGE}
    if args.suite == 'integration':
        images.update({'PG_IMAGE': 'postgres:18.1-bookworm', 'REDIS_IMAGE': 'redis:8.4-bookworm'})
    resolved = {}
    for argument, tag in images.items():
        run(['docker', 'pull', tag])
        info = json.loads(output(['docker', 'image', 'inspect', tag]))[0]
        repository = tag.split(':')[0]
        digests = [d for d in info['RepoDigests'] if d.startswith(repository+'@sha256:')]
        if len(digests) != 1:
            raise RuntimeError('ambiguous dependency image digest: '+repository)
        resolved[argument] = digests[0]
    build = ['docker', 'build']
    for argument, digest in resolved.items():
        build += ['--build-arg', argument+'='+digest]
    run([*build, '--iidfile', str(work / 'image-id'), str(context)])
    image_id = (work / 'image-id').read_text().strip()
    if not image_id.startswith('sha256:') or len(image_id) != 71:
        raise RuntimeError('invalid immutable test image ID')
    manifest_sha = sha(context / 'source-manifest.json')
    env = {'PATH': '/usr/local/go/bin:/usr/bin:/bin', 'HOME': '/tmp/home', 'TMPDIR': '/tmp',
           'GOCACHE': '/tmp/gocache', 'GOTMPDIR': '/tmp/gotmp', 'GOMODCACHE': '/opt/gomodcache',
           'GOPATH': '/tmp/gopath', 'GOPROXY': 'off', 'GOSUMDB': 'off', 'GOTOOLCHAIN': 'local',
           'GOMAXPROCS': '2', 'GOGC': '30', 'GOMEMLIMIT': '3GiB', 'CI': 'true'}
    command = ['/usr/bin/env', '-i', *[k+'='+v for k, v in env.items()],
               '/usr/bin/python3', '-I', '/opt/isolation/isolated-unit-entrypoint.py',
               '--source-root', '/src', '--manifest', '/opt/isolation/source-manifest.json',
               '--manifest-sha256', manifest_sha, '--suite', args.suite]
    cid = output(['docker', 'create', '--network', 'none', '--user', '65532:65532',
                  '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges', '--read-only',
                  '--ipc', 'private', '--cgroupns', 'private', '--pids-limit', '256',
                  '--memory', '6g', '--cpus', '2',
                  '--tmpfs', '/tmp:rw,exec,nosuid,nodev,size=4294967296,uid=65532,gid=65532,mode=1777',
                  '--entrypoint', command[0], image_id, *command[1:]])
    try:
        data = json.loads(output(['docker', 'inspect', cid]))[0]
        checks = assert_container(data, image_id, command)
        receipt = {'source_commit': source['source_commit'], 'source_manifest_sha256': manifest_sha,
                   'base_image_digest': resolved['GO_IMAGE'], 'dependency_image_digests': resolved, 'suite': args.suite, 'image_id': image_id, 'container_id': cid,
                   'host_preflight': checks, 'network_mode': data['HostConfig']['NetworkMode'],
                   'test_environment_keys': sorted(env), 'test_exit_code': None}
        (work / 'host-receipt.json').write_text(json.dumps(receipt, indent=2)+'\n')
        try:
            with (work / 'runtime-output.jsonl').open('w') as stream:
                result = subprocess.run(['docker', 'start', '--attach', cid], timeout=1800,
                                        stdout=stream, stderr=subprocess.STDOUT)
            exit_code = result.returncode
        except subprocess.TimeoutExpired:
            subprocess.run(['docker', 'stop', '--time', '5', cid], check=False)
            exit_code = 124
        # Attach command errors cannot turn a nonzero test/container into success.
        state = json.loads(output(['docker', 'inspect', cid]))[0]['State']
        if state['Running']:
            raise RuntimeError('container did not terminate')
        exit_code = exit_code or state['ExitCode']
        receipt['test_exit_code'] = exit_code
        receipt['container_exit_code'] = state['ExitCode']
        receipt['oom_killed'] = state['OOMKilled']
        (work / 'host-receipt.json').write_text(json.dumps(receipt, indent=2)+'\n')
        run(['docker', 'cp', cid+':/opt/isolation/modules.json', str(work / 'modules.json')])
        run(['docker', 'cp', cid+':/opt/isolation/os-packages.txt', str(work / 'os-packages.txt')])
        runtime = collect_stream(work, source['source_commit'], manifest_sha, args.suite)
        if runtime['test_exit_code'] != state['ExitCode']:
            raise RuntimeError('test exit status does not match container status')
        raise SystemExit(exit_code)
    finally:
        subprocess.run(['docker', 'rm', '--force', cid], check=False, stdout=subprocess.DEVNULL)


if __name__ == '__main__':
    main()
