import copy
import json
import re
import tempfile
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('host', Path(__file__).parents[1] / 'scripts/run-isolated-unit.py')
host = importlib.util.module_from_spec(spec)
spec.loader.exec_module(host)


class HostPreflight(unittest.TestCase):
    def setUp(self):
        self.data = {'Image': 'sha256:fixed', 'Config': {'User': '65532:65532'},
                     'Path': '/usr/bin/env', 'Args': ['-i', 'approved'], 'Mounts': [],
                     'HostConfig': {'NetworkMode': 'none', 'Privileged': False,
                                    'ReadonlyRootfs': True, 'CapDrop': ['ALL'],
                                    'SecurityOpt': ['no-new-privileges'],
                                    'IpcMode': 'private', 'CgroupnsMode': 'private',
                                    'Tmpfs': {'/tmp': 'rw,exec,nosuid,nodev'}}}
        self.command = ['/usr/bin/env', '-i', 'approved']

    def check(self, data):
        return host.assert_container(data, 'sha256:fixed', self.command)

    def test_valid_exact_container(self):
        self.assertTrue(all(self.check(self.data).values()))

    def test_unsafe_container_variants_rejected_before_start(self):
        changes = [('NetworkMode', 'bridge'), ('Privileged', True), ('ReadonlyRootfs', False),
                   ('CapDrop', []), ('CapAdd', ['NET_ADMIN']), ('SecurityOpt', []),
                   ('PidMode', 'host'), ('IpcMode', 'host'), ('UTSMode', 'host'),
                   ('CgroupnsMode', 'host'), ('Binds', ['/var/run/docker.sock:/var/run/docker.sock']),
                   ('Devices', ['device']), ('ExtraHosts', ['cloud:host-gateway']),
                   ('PortBindings', {'80/tcp': []}), ('PublishAllPorts', True),
                   ('Tmpfs', {'/tmp': 'rw', '/secret': 'rw'})]
        for key, value in changes:
            with self.subTest(key=key):
                data = copy.deepcopy(self.data)
                data['HostConfig'][key] = value
                with self.assertRaises(RuntimeError):
                    self.check(data)

    def test_wrong_image_user_command_and_mount_rejected(self):
        for key, value in [('Image', 'sha256:other'), ('Path', '/bin/sh'),
                           ('Args', ['-c', 'unsafe']), ('Config', {'User': 'root'}),
                           ('Mounts', [{'Type': 'bind', 'Destination': '/tmp'}])]:
            with self.subTest(key=key):
                data = copy.deepcopy(self.data)
                data[key] = value
                with self.assertRaises(RuntimeError):
                    self.check(data)


class StreamEvidence(unittest.TestCase):
    def records(self, suite='unit'):
        fixed = list(host.FIXED_SERVICE_TESTS if suite == 'unit' else host.FIXED_SERVICE_TESTS[-1:])
        main = ['TestMainCase', 'ExampleCatalog', 'FuzzSeed']
        names = sorted(main + fixed)
        partitions = [{'name': 'main', 'tests': main, 'regex': '^('+'|'.join(main)+')$'}]
        partitions += [{'name': n, 'tests': [n], 'regex': '^'+re.escape(n)+'$'} for n in fixed]
        first = {'record_type': 'isolation_receipt', 'isolation_preflight': 'passed',
                 'suite': suite, 'source_commit': 'a'*40, 'manifest_sha256': 'b'*64, 'test_exit_code': None}
        last = dict(first, test_exit_code=0, fixed_service_tests=fixed,
                    service_partition_audit={'package': host.SERVICE_PACKAGE, 'binary_sha256': 'c'*64,
                        'discovery_exit_code': 0, 'discovered_tests_examples_fuzz': names, 'partitions': partitions},
                    package_results=[{'package': host.SERVICE_PACKAGE, 'partition': p['name'],
                        'binary_sha256': 'c'*64, 'raw_exit_code': 0, 'exit_code': 0} for p in partitions],
                    fixture_receipt={'cleanup': 'stopped', 'postgres_ready': True})
        return [first, *[{'Action': 'run', 'Package': host.SERVICE_PACKAGE, 'Test': n} for n in names],
                {'Action': 'pass', 'Package': 'example', 'Test': 'TestOne'}, last]

    def collect(self, records, suite='unit'):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        work = Path(directory.name)
        (work/'runtime-output.jsonl').write_text(''.join(json.dumps(x)+'\n' for x in records))
        return work, lambda: host.collect_stream(work, 'a'*40, 'b'*64, suite)

    def test_exact_actual_coverage_both_suites_and_explicit_skips(self):
        for suite in ('unit', 'integration'):
            records = self.records(suite)
            records.insert(-1, {'Action': 'skip', 'Package': host.SERVICE_PACKAGE, 'Test': 'FuzzSeed'})
            records.insert(-1, {'Action': 'run', 'Package': host.SERVICE_PACKAGE, 'Test': 'FuzzSeed/seed#0'})
            _, collect = self.collect(records, suite)
            receipt = collect()
            self.assertEqual(receipt['actual_service_coverage']['status'], 'passed')
            self.assertEqual(receipt['test_event_counts'], {'pass': 1, 'fail': 0, 'skip': 1})
            self.assertEqual(receipt['skipped_tests'][0]['test'], 'FuzzSeed')

    def test_actual_coverage_missing_duplicate_and_unknown_fail_with_persisted_evidence(self):
        for kind in ('missing', 'duplicate', 'unknown'):
            records = self.records()
            if kind == 'missing':
                records.pop(1)
            elif kind == 'duplicate':
                records.insert(1, dict(records[1]))
            else:
                records.insert(1, {'Action': 'run', 'Package': host.SERVICE_PACKAGE, 'Test': 'TestUnknown'})
            work, collect = self.collect(records)
            with self.subTest(kind=kind), self.assertRaises(RuntimeError):
                collect()
            persisted = json.loads((work/'isolation-receipt.json').read_text())
            self.assertEqual(persisted['actual_service_coverage']['status'], 'failed')
            self.assertEqual(persisted['test_event_counts']['pass'], 1)
            self.assertTrue(persisted['actual_service_coverage']['errors'])

    def test_missing_audit_and_bad_terminal_partition_evidence_fail_closed(self):
        for kind in ('audit', 'missing', 'duplicate', 'hash', 'exit', 'fixed', 'membership'):
            records = self.records()
            last = records[-1]
            if kind == 'audit':
                del last['service_partition_audit']
            elif kind == 'missing':
                last['package_results'].pop()
            elif kind == 'duplicate':
                last['package_results'].append(dict(last['package_results'][0]))
            elif kind == 'hash':
                last['package_results'][0]['binary_sha256'] = 'd'*64
            elif kind == 'exit':
                del last['package_results'][0]['exit_code']
            elif kind == 'fixed':
                last['fixed_service_tests'] = []
            else:
                last['service_partition_audit']['partitions'][0]['tests'].pop()
            work, collect = self.collect(records)
            with self.subTest(kind=kind), self.assertRaises(RuntimeError):
                collect()
            self.assertEqual(json.loads((work/'isolation-receipt.json').read_text())['actual_service_coverage']['status'], 'failed')

    def test_preserves_stopped_container_receipts_and_reports_skips(self):
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            records = self.records()
            records.insert(-1, {'Action':'skip','Package':'example','Test':'TestInheritedSkip'})
            (work/'runtime-output.jsonl').write_text(''.join(json.dumps(x)+'\n' for x in records))
            receipt = host.collect_stream(work, 'a'*40, 'b'*64)
            self.assertEqual(receipt['test_event_counts'], {'pass':1,'fail':0,'skip':1})
            self.assertEqual(len(receipt['skipped_tests']), 1)
            self.assertNotIn('isolation_receipt', (work/'unit-tests.jsonl').read_text())

    def test_missing_tampered_empty_or_duplicate_receipts_fail_closed(self):
        first = {'record_type':'isolation_receipt','isolation_preflight':'passed',
                 'suite':'unit','source_commit':'a'*40,'manifest_sha256':'b'*64,'test_exit_code':None}
        last = dict(first, test_exit_code=0)
        passed = {'Action':'pass','Package':'example','Test':'TestOne'}
        for records in ([first, passed], [first,last], [first,passed,last,last],
                        [dict(first,source_commit='c'*40),passed,last],
                        [first,passed,dict(last,manifest_sha256='c'*64)]):
            with self.subTest(records=len(records)), tempfile.TemporaryDirectory() as directory:
                work=Path(directory)
                (work/'runtime-output.jsonl').write_text(''.join(json.dumps(x)+'\n' for x in records))
                with self.assertRaises(RuntimeError):
                    host.collect_stream(work,'a'*40,'b'*64)
