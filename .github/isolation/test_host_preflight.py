import copy
import json
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
    def test_preserves_stopped_container_receipts_and_reports_skips(self):
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            first = {'record_type': 'isolation_receipt', 'isolation_preflight': 'passed',
                     'suite':'unit', 'source_commit': 'a'*40, 'manifest_sha256': 'b'*64, 'test_exit_code': None}
            last = dict(first, test_exit_code=0)
            records = [first, {'Action':'pass','Package':'example','Test':'TestOne'},
                       {'Action':'skip','Package':'example','Test':'TestInheritedSkip'}, last]
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
