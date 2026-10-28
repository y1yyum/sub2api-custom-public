#!/usr/bin/env python3
"""Pure Python fixture tests; never runs Docker, Go, DNS or external requests."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock

spec = importlib.util.spec_from_file_location('entrypoint', Path(__file__).with_name('isolated-unit-entrypoint.py'))
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)

STATUS = '\n'.join(['Uid:\t65532 65532 65532 65532', 'Gid:\t65532 65532 65532 65532', 'Groups:\t65532',
                    *[key + ':\t0000000000000000' for key in ('CapInh', 'CapPrm', 'CapEff', 'CapBnd', 'CapAmb')], 'NoNewPrivs:\t1'])
V4 = 'Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT\n'
V6_LOOP = '00000000000000000000000000000001 80 00000000000000000000000000000000 00 00000000000000000000000000000000 00000000 00000000 00000000 80200001 lo\n'
V6_REJECT = '00000000000000000000000000000000 00 00000000000000000000000000000000 00 00000000000000000000000000000000 ffffffff 00000001 00000000 00200200 lo\n'
ADDR6 = '00000000000000000000000000000001 01 80 10 80 lo\n'
MOUNTS = '1 0 0:1 / / ro,relatime - overlay overlay ro\n2 1 0:2 / /tmp rw,nosuid,nodev - tmpfs tmpfs rw\n'


class RuntimeFixtures(unittest.TestCase):
    def assert_blocks_execution(self, validator):
        execute = Mock(side_effect=AssertionError('subprocess must not be reached'))
        with self.assertRaises(m.IsolationError):
            m.run(Path('/src'), Path('/opt/isolation/source-manifest.json'), '0' * 64,
                  inspect=lambda *_: validator(), execute=execute)
        execute.assert_not_called()

    def test_clean_environment_and_offline_child_environment(self):
        m.check_environment({'PATH': '/usr/local/go/bin:/usr/bin:/bin', 'HOME': '/tmp/home', 'GOPROXY': 'off'})
        child = m.test_environment()
        self.assertEqual(child['GOPROXY'], 'off')
        self.assertEqual(child['GOSUMDB'], 'off')
        self.assertEqual(child['GOTOOLCHAIN'], 'local')
        self.assertEqual(child['GOENV'], 'off')
        self.assertNotIn('HTTPS_PROXY', child)
        self.assertNotIn('GOFLAGS', child)

    def test_inherited_secret_proxy_or_toolchain_override_blocks_before_any_subprocess(self):
        for env in ({'AWS_SECRET_ACCESS_KEY': 'not-a-real-secret'}, {'HTTPS_PROXY': 'http://invalid'},
                    {'GOFLAGS': '-overlay=evil'}, {'GOPROXY': 'https://invalid'},
                    {'GOTOOLCHAIN': 'auto'}, {'PATH': '/tmp/bin'}):
            with self.subTest(keys=list(env)):
                self.assert_blocks_execution(lambda: m.check_environment(env))

    def test_unprivileged_status(self):
        m.check_status(STATUS)

    def test_all_capability_sets_and_no_new_privileges(self):
        for key in ('CapInh', 'CapPrm', 'CapEff', 'CapBnd', 'CapAmb'):
            bad = STATUS.replace(key + ':\t0000000000000000', key + ':\t0000000000000001')
            self.assert_blocks_execution(lambda: m.check_status(bad))
        for bad in (STATUS.replace('NoNewPrivs:\t1', 'NoNewPrivs:\t0'), STATUS.replace('65532 65532 65532 65532', '0 0 0 0'), STATUS.replace('Groups:\t65532', 'Groups:\t0')):
            self.assert_blocks_execution(lambda: m.check_status(bad))

    def test_loopback_and_unreachable_ipv6_defaults_allowed(self):
        m.check_network(['lo'], V4, V6_LOOP + V6_REJECT, ADDR6, '127.0.0.1')
        m.check_network(['lo'], V4, '', '', '127.0.0.1')

    def test_interfaces_addresses_and_routes_reject_before_any_subprocess(self):
        cases = [(['lo', 'eth0'], V4, '', '', '127.0.0.1'),
                 (['lo'], V4, '', '', '10.0.0.1'),
                 (['lo'], V4 + 'lo 00000000 00000000 0001 0 0 0 00000000 0 0 0\n', '', '', '127.0.0.1'),
                 (['lo'], V4, V6_REJECT.replace('00200200', '00000001'), '', '127.0.0.1'),
                 (['lo'], V4, V6_LOOP.replace(' lo', ' eth0'), '', '127.0.0.1'),
                 (['lo'], V4, '', ADDR6.replace('00000000000000000000000000000001', '20010000000000000000000000000001'), '127.0.0.1')]
        for args in cases:
            self.assert_blocks_execution(lambda: m.check_network(*args))

    def test_mount_contract(self):
        m.check_mounts(MOUNTS)
        for bad in (MOUNTS.replace('/ ro,', '/ rw,'), MOUNTS.replace('/tmp rw,nosuid,nodev', '/tmp rw,noexec,nosuid,nodev'), MOUNTS + '3 1 0:3 / /root/.aws ro - ext4 /dev/x ro\n', MOUNTS + '3 1 0:3 / /var/run/docker.sock rw - ext4 /dev/x rw\n'):
            self.assert_blocks_execution(lambda: m.check_mounts(bad))

    def source_fixture(self, directory):
        root = Path(directory) / 'src'
        (root / 'backend').mkdir(parents=True)
        (root / 'backend/go.mod').write_text('module example.invalid/test\n\ngo 1.27.3\n')
        receipt = {'tested_commit': 'a' * 40, 'working_tree_diff': '',
                   'source_sha256': {'backend/go.mod': m.digest((root / 'backend/go.mod').read_bytes()), 'frontend/unshipped.ts': 'b' * 64}}
        (root / 'validation-receipt.json').write_text(json.dumps(receipt))
        doc = {'schema': 1, 'source_commit': 'a' * 40,
               'source_receipt_sha256': m.digest((root / 'validation-receipt.json').read_bytes()),
               'files': {p.relative_to(root).as_posix(): m.digest(p.read_bytes()) for p in root.rglob('*') if p.is_file()}}
        manifest = Path(directory) / 'manifest.json'
        manifest.write_text(json.dumps(doc))
        return root, manifest, m.digest(manifest.read_bytes())

    def test_complete_source_receipt(self):
        with tempfile.TemporaryDirectory() as directory:
            root, manifest, sha = self.source_fixture(directory)
            self.assertEqual(m.verify_sources(root, manifest, sha)['source_commit'], 'a' * 40)

    def test_source_or_manifest_digest_tampering_blocks_before_subprocess(self):
        for change in ('source', 'manifest', 'extra', 'symlink', 'removed'):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as directory:
                root, manifest, sha = self.source_fixture(directory)
                if change == 'source':
                    (root / 'backend/go.mod').write_text('tampered')
                elif change == 'manifest':
                    sha = '0' * 64
                elif change == 'extra':
                    (root / 'backend/extra.go').write_text('package extra')
                elif change == 'symlink':
                    (root / 'backend/link').symlink_to('go.mod')
                else:
                    (root / 'backend/go.mod').unlink()
                self.assert_blocks_execution(lambda: m.verify_sources(root, manifest, sha))

    def test_test_command_has_no_coverage_reducing_filters(self):
        self.assertEqual(m.TEST_FLAGS, ['-test.v=test2json', '-test.timeout=20m', '-test.count=1'])
        command = m.binary_command({'package': 'example.invalid/test', 'binary': 'binaries/fake.test'})
        self.assertEqual(command[:3], [m.GO, 'tool', 'test2json'])
        self.assertNotIn('-run', command)
        self.assertNotIn('-skip', command)


if __name__ == '__main__':
    unittest.main()
