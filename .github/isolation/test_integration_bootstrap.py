"""Pure Python lifecycle fixtures: subprocesses are always mocks, never services."""
import importlib.util
from pathlib import Path
import subprocess
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

SCRIPT = Path(__file__).resolve().parents[1] / 'scripts/integration-bootstrap.py'
spec = importlib.util.spec_from_file_location('integration_bootstrap', SCRIPT)
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


class BootstrapTests(unittest.TestCase):
    def setup_fixture(self, fail=None):
        calls = []
        paths = []

        def execute(argv, **kwargs):
            calls.append((argv, kwargs))
            command = Path(argv[0]).name
            result = SimpleNamespace(returncode=0, stdout='', stderr='')
            if command == 'go':
                result.stdout = 'go version go1.27.3 linux/amd64\n'
            elif command == 'postgres':
                result.stdout = 'postgres (PostgreSQL) 18.1 (Debian 18.1-1.pgdg12+1)\n'
            elif command == 'redis-server':
                result.stdout = 'Redis server v=8.4.2 sha=00000000:0 malloc=jemalloc-5.3.0 bits=64 build=fixture\n'
            elif command == 'initdb':
                directory = Path(argv[argv.index('-D') + 1])
                paths.append(directory.parent)
                directory.mkdir()
                (directory / 'postgresql.conf').write_text('# mock initdb\n')
                password = Path(next(a.split('=', 1)[1] for a in argv if a.startswith('--pwfile=')))
                self.assertEqual(password.stat().st_mode & 0o777, 0o600)
                self.assertEqual(password.read_text(), 'fixture-only\n')
            elif command == 'psql':
                result.stdout = argv[argv.index('-d') + 1] + '\n'
            if fail:
                fail(argv, result)
            return result

        return Mock(side_effect=execute), calls, paths

    def run_fixture(self, execute, callback=None, inspect=None):
        return m.run_with_postgres(Path('/src'), Path('/opt/isolation/source-manifest.json'), 'a' * 64,
                                   callback or (lambda *_: 0),
                                   inspect=inspect or Mock(return_value={'source_commit': 'b' * 40}), execute=execute)

    def test_preflight_failure_executes_nothing(self):
        execute = Mock(side_effect=AssertionError('no process before preflight'))
        with self.assertRaises(m.shared.IsolationError):
            self.run_fixture(execute, inspect=Mock(side_effect=m.shared.IsolationError('blocked')))
        execute.assert_not_called()

    def test_preflight_precedes_all_versions_and_processes(self):
        execute, calls, _ = self.setup_fixture()
        inspected = []
        def inspect(*_):
            self.assertEqual(calls, [])
            inspected.append(True)
            return {'source_commit': 'b' * 40}
        self.run_fixture(execute, inspect=inspect)
        self.assertEqual(inspected, [True])
        self.assertEqual([Path(c[0][0]).name for c in calls[:3]], ['go', 'postgres', 'redis-server'])

    def test_fixed_version_mismatch_never_initializes_database(self):
        for binary, bad in [('go', 'go version go1.27.4 linux/amd64\n'),
                            ('postgres', 'postgres (PostgreSQL) 18.2\n'),
                            ('redis-server', 'Redis server v=8.5.0 bits=64\n')]:
            def fail(argv, result):
                if Path(argv[0]).name == binary:
                    result.stdout = bad
            execute, calls, _ = self.setup_fixture(fail)
            with self.subTest(binary=binary), self.assertRaises(m.shared.IsolationError):
                self.run_fixture(execute)
            self.assertFalse(any(Path(c[0][0]).name == 'initdb' for c in calls))

    def test_authenticated_readiness_two_distinct_databases_and_cleanup(self):
        execute, calls, paths = self.setup_fixture()
        def callback(doc, env, receipt):
            self.assertTrue(receipt['postgres_ready'])
            self.assertEqual(receipt['databases'], list(m.DATABASES))
            self.assertEqual(env['SUB2API_ISOLATED_INTEGRATION'], '1')
            self.assertNotIn('PGPASSWORD', env)
            self.assertNotIn('DATABASE_URL', env)
            self.assertNotIn('HTTPS_PROXY', env)
            self.assertEqual(env['GOPROXY'], 'off')
            self.assertFalse((paths[0] / 'password').exists())
            conf = (paths[0] / 'data/postgresql.conf').read_text()
            self.assertIn("listen_addresses = '127.0.0.1'", conf)
            self.assertIn("unix_socket_directories = ''", conf)
            return 7
        result, receipt = self.run_fixture(execute, callback)
        self.assertEqual(result, 7)  # preserve test failures, never convert to green
        self.assertEqual(receipt['cleanup'], 'stopped')
        databases = [c[0][c[0].index('-d') + 1] for c in calls if Path(c[0][0]).name == 'psql']
        self.assertEqual(databases, list(m.DATABASES))
        self.assertEqual(calls[-1][0][-1], 'stop')
        self.assertTrue(all(not p.exists() for p in paths))
        for argv, kwargs in calls:
            self.assertFalse(kwargs['check'])
            self.assertNotIn('shell', kwargs)
            self.assertEqual(kwargs['cwd'], Path('/src/backend'))

    def test_readiness_failure_never_runs_tests_and_stops(self):
        def fail(argv, result):
            if Path(argv[0]).name == 'psql':
                result.stdout = 'wrong_database\n'
        execute, calls, paths = self.setup_fixture(fail)
        callback = Mock()
        with self.assertRaises(m.shared.IsolationError):
            self.run_fixture(execute, callback)
        callback.assert_not_called()
        self.assertEqual(calls[-1][0][-1], 'stop')
        self.assertTrue(all(not p.exists() for p in paths))

    def test_failed_database_creation_never_runs_tests_and_stops(self):
        def fail(argv, result):
            if Path(argv[0]).name == 'createdb':
                result.returncode = 1
        execute, calls, _ = self.setup_fixture(fail)
        callback = Mock()
        with self.assertRaises(m.shared.IsolationError):
            self.run_fixture(execute, callback)
        callback.assert_not_called()
        self.assertEqual(calls[-1][0][-1], 'stop')

    def test_immediate_cleanup_fallback_is_reported_not_disguised_as_graceful(self):
        def fail(argv, result):
            if Path(argv[0]).name == 'pg_ctl' and argv[-1] == 'stop' and 'fast' in argv:
                result.returncode = 1
        execute, _, _ = self.setup_fixture(fail)
        _, receipt = self.run_fixture(execute)
        self.assertEqual(receipt['cleanup'], 'stopped-immediate')

    def test_callback_exception_always_stops(self):
        execute, calls, _ = self.setup_fixture()
        with self.assertRaisesRegex(RuntimeError, 'mock assertion'):
            self.run_fixture(execute, Mock(side_effect=RuntimeError('mock assertion')))
        self.assertEqual(calls[-1][0][-1], 'stop')

    def test_startup_timeout_still_attempts_cleanup(self):
        def fail(argv, result):
            if Path(argv[0]).name == 'pg_ctl' and argv[-1] == 'start':
                raise subprocess.TimeoutExpired(argv, 40)
        execute, calls, _ = self.setup_fixture(fail)
        callback = Mock()
        with self.assertRaises(subprocess.TimeoutExpired):
            self.run_fixture(execute, callback)
        callback.assert_not_called()
        self.assertEqual(calls[-1][0][-1], 'stop')

    def test_failed_cleanup_fails_closed_and_attempts_immediate_stop(self):
        def fail(argv, result):
            if Path(argv[0]).name == 'pg_ctl' and argv[-1] == 'stop':
                result.returncode = 1
        execute, calls, _ = self.setup_fixture(fail)
        with self.assertRaises(m.shared.IsolationError):
            self.run_fixture(execute)
        modes = [c[0][c[0].index('-m') + 1] for c in calls if c[0][-1] == 'stop']
        self.assertEqual(modes, ['fast', 'immediate'])

    def test_integration_requires_exact_fixture_identity_after_shared_preflight(self):
        with patch.object(m.shared, 'preflight', return_value={}) as preflight, \
             patch.object(m.os, 'geteuid', return_value=123), patch.object(m.os, 'getegid', return_value=65532):
            with self.assertRaises(m.shared.IsolationError):
                m.integration_preflight(Path('/src'), Path('/opt/isolation/source-manifest.json'), 'a' * 64)
            preflight.assert_called_once()


if __name__ == '__main__':
    unittest.main()
