"""Pure partition/audit tests; no test binaries, Go or Docker are executed."""
import contextlib
import importlib.util
import io
from pathlib import Path
import re
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

spec = importlib.util.spec_from_file_location('partition_runtime', Path(__file__).resolve().parents[1] / 'scripts/isolated-unit-entrypoint.py')
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


class FixedPartitionTests(unittest.TestCase):
    def listing(self, suite='unit'):
        return '\n'.join(['TestOrdinary', 'TestOrdinaryLonger', 'Example', 'ExampleFeature', 'FuzzSeeds', 'BenchmarkNotRun',
                          *m.FIXED_SERVICE_TESTS_BY_SUITE[suite]]) + '\n'

    def test_unit_and_integration_coverage_is_exact_exhaustive_and_disjoint(self):
        for suite, count in [('unit', 3), ('integration', 2)]:
            with self.subTest(suite=suite):
                audit = m.service_partitions(self.listing(suite), suite)
                self.assertEqual(len(audit['partitions']), count)
                self.assertEqual(audit['benchmarks_not_run_by_default'], ['BenchmarkNotRun'])
                names = audit['discovered_tests_examples_fuzz']
                for name in names:
                    self.assertEqual(sum(bool(re.fullmatch(p['regex'], name)) for p in audit['partitions']), 1)
                for name in ['TestOrdinaryExtra', 'BenchmarkNotRun', 'Unlisted']:
                    self.assertFalse(any(re.fullmatch(p['regex'], name) for p in audit['partitions']))
                self.assertIn('Example', audit['partitions'][0]['tests'])
                self.assertIn('FuzzSeeds', audit['partitions'][0]['tests'])

    def test_absent_renamed_duplicate_and_ambiguous_registry_fail_closed(self):
        listing = self.listing()
        for bad in [listing.replace(m.FIXED_SERVICE_TESTS[0], 'TestRenamed'),
                    listing + 'TestOrdinary\n', listing + 'unexpected registry diagnostic\n',
                    listing.replace('TestOrdinary\n', 'TestOrdinary/subtest\n')]:
            with self.subTest(bad=bad[-35:]), self.assertRaises(m.IsolationError):
                m.service_partitions(bad, 'unit')
        with self.assertRaises(m.IsolationError):
            m.service_partitions(self.listing('unit'), 'integration')

    def test_trie_stays_bounded_without_process_chunking(self):
        names = ['TestSharedPrefixCase' + str(i).zfill(5) for i in range(10000)]
        expression = m.exact_trie_regex(names)
        self.assertLess(len(expression.encode()), 65536)
        for name in names:
            self.assertIsNotNone(re.fullmatch(expression, name))
        self.assertIsNone(re.fullmatch(expression, 'TestSharedPrefixCase10000'))
        with self.assertRaises(m.IsolationError):
            m.exact_trie_regex(['Test' + 'x' * 600])

    def run_fixture(self, process_codes):
        record = {'package': m.SERVICE_PACKAGE, 'directory': 'backend/internal/service',
                  'binary': 'binaries/service.test', 'binary_sha256': 'a' * 64}
        compiled = {'package_count': 1, 'packages': [record]}
        listed = SimpleNamespace(returncode=0, stdout=self.listing(), stderr='initialization diagnostic')
        execute = Mock(side_effect=[listed, *[SimpleNamespace(returncode=code) for code in process_codes]])
        doc = {'source_commit': 'b' * 40, 'source_receipt_sha256': 'c' * 64, 'files': {}}
        env = m.test_environment()
        with patch.object(m, 'verify_compiled', return_value=(compiled, 'd' * 64)), contextlib.redirect_stdout(io.StringIO()):
            receipt = m.execute_suite(Path('/src'), doc, env, 'e' * 64, 'unit', execute)
        return receipt, execute, env

    def test_schedule_identical_before_success_or_failure_and_no_retries(self):
        green, green_calls, _ = self.run_fixture([0, 0, 0])
        red, red_calls, _ = self.run_fixture([1, 0, 1])
        self.assertEqual(green['test_commands'], red['test_commands'])
        self.assertEqual(green_calls.call_count, 4)
        self.assertEqual(red_calls.call_count, 4)
        self.assertEqual(green['test_exit_code'], 0)
        self.assertEqual(red['test_exit_code'], 1)
        self.assertEqual([p['exit_code'] for p in red['package_results']], [1, 0, 1])
        self.assertEqual([p['partition'] for p in red['package_results']], ['main', *m.FIXED_SERVICE_TESTS])
        for call in red_calls.call_args_list:
            self.assertEqual(call.kwargs['cwd'], Path('/src/backend/internal/service'))
            self.assertIn('/opt/isolation/compiled/binaries/service.test', call.args[0])
        for call in red_calls.call_args_list[1:]:
            self.assertIn('-test.timeout=20m', call.args[0])
            self.assertIn('-test.count=1', call.args[0])
            self.assertEqual(sum(arg.startswith('-test.run=') for arg in call.args[0]), 1)

    def test_synthetic_public_client_id_is_service_child_only_before_listing_init(self):
        receipt, calls, env = self.run_fixture([0, 0, 0])
        self.assertNotIn('GEMINI_CLI_OAUTH_CLIENT_ID', env)
        self.assertEqual(receipt['synthetic_service_child_environment'], {'GEMINI_CLI_OAUTH_CLIENT_ID': 'custom-test-client'})
        for call in calls.call_args_list:
            self.assertEqual(call.kwargs['env']['GEMINI_CLI_OAUTH_CLIENT_ID'], 'custom-test-client')
            self.assertEqual(call.kwargs['env']['GOPROXY'], 'off')
            self.assertNotIn('GEMINI_CLI_OAUTH_CLIENT_SECRET', call.kwargs['env'])
        self.assertEqual(m.package_environment({'package': 'example/other'}, env), env)
        with self.assertRaises(m.IsolationError):
            m.check_environment({'GEMINI_CLI_OAUTH_CLIENT_ID': 'custom-test-client'})

    def test_inventory_error_prevents_all_partition_execution(self):
        record = {'package': m.SERVICE_PACKAGE, 'directory': 'backend/internal/service',
                  'binary': 'binaries/service.test', 'binary_sha256': 'a' * 64}
        execute = Mock(return_value=SimpleNamespace(returncode=0, stdout='TestMissingFixed\n', stderr=''))
        doc = {'source_commit': 'b' * 40, 'source_receipt_sha256': 'c' * 64, 'files': {}}
        with patch.object(m, 'verify_compiled', return_value=({'package_count': 1, 'packages': [record]}, 'd' * 64)), contextlib.redirect_stdout(io.StringIO()), self.assertRaises(m.IsolationError):
            m.execute_suite(Path('/src'), doc, m.test_environment(), 'e' * 64, 'unit', execute)
        self.assertEqual(execute.call_count, 1)
        self.assertIn('-test.list=.', execute.call_args.args[0])


if __name__ == '__main__':
    unittest.main()
