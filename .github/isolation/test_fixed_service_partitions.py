"""Pure partition/audit tests; no test binaries, Go or Docker are executed."""
import contextlib
import importlib.util
import io
import itertools
import hashlib
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
        for suite, count in [('unit', 5), ('integration', 2)]:
            with self.subTest(suite=suite):
                audit = m.service_partitions(self.listing(suite), suite)
                self.assertEqual(len(audit['partitions']), count)
                self.assertEqual(audit['benchmarks_not_run_by_default'], ['BenchmarkNotRun'])
                names = audit['discovered_tests_examples_fuzz']
                for name in names:
                    self.assertEqual(sum(bool(re.fullmatch(p['regex'], name)) for p in audit['partitions']), 1)
                # Main regex is the complement of only the fixed names; the
                # host separately rejects unexpected actual registry/run events.
                self.assertTrue(re.fullmatch(audit['partitions'][0]['regex'], 'TestOrdinaryExtra'))
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

    def test_all_three_timezone_mutators_are_required_unit_only(self):
        expected = {'TestGroupUsageDateUsesConfiguredTimezoneBoundary',
                    'TestGroupUsageParseDateUsesConfiguredTimezone',
                    'TestGroupUsageYesterdayStartHandlesDST'}
        self.assertEqual(set(m.FIXED_SERVICE_TESTS[:-1]), expected)
        for name in expected:
            with self.assertRaises(m.IsolationError):
                m.service_partitions(self.listing().replace(name + '\n', ''), 'unit')
            with self.assertRaises(m.IsolationError):
                m.service_partitions(self.listing('integration') + name + '\n', 'integration')

    def test_complement_exhaustive_small_alphabet_including_prefix_exclusions(self):
        for excluded in [('a',), ('a', 'ab'), ('ab', 'ac'), ('abc', 'bba')]:
            expression = m.fixed_exclusion_regex(excluded)
            for length in range(6):
                for chars in itertools.product('abc_', repeat=length):
                    name = ''.join(chars)
                    self.assertEqual(bool(re.fullmatch(expression, name)), name not in excluded, (excluded, name))
            self.assertNotIn('(?', expression)  # No unsupported RE2 lookarounds.

    def test_fixed_name_neighbors_prefixes_suffixes_and_character_changes(self):
        for fixed in m.FIXED_SERVICE_TESTS_BY_SUITE.values():
            expression = m.fixed_exclusion_regex(fixed)
            for name in fixed:
                neighbors = {name[:i] for i in range(len(name) + 1)}
                neighbors.update({name + 'Extra', 'TestPrefix' + name, name + '_', name + '0'})
                for i in range(len(name)):
                    neighbors.add(name[:i] + name[i + 1:])
                    for char in 'aZ_0':
                        neighbors.add(name[:i] + char + name[i + 1:])
                for other in neighbors:
                    self.assertEqual(bool(re.fullmatch(expression, other)), other not in fixed, other)

    def test_8300_diverse_names_keep_fixed_small_regex_without_process_chunking(self):
        names = ['Test' + hashlib.sha256(str(i).encode()).hexdigest() for i in range(8300)]
        for suite, fixed in m.FIXED_SERVICE_TESTS_BY_SUITE.items():
            audit = m.service_partitions('\n'.join(names + list(fixed)), suite)
            expression = audit['partitions'][0]['regex']
            self.assertLess(len(expression.encode()), 4096)
            self.assertEqual(expression, m.fixed_exclusion_regex(fixed))
            self.assertEqual(len(audit['partitions']), 1 + len(fixed))
            self.assertEqual(set(audit['partitions'][0]['tests']), set(names))
            for name in names:
                self.assertIsNotNone(re.fullmatch(expression, name))
            for name in fixed:
                self.assertIsNone(re.fullmatch(expression, name))
        with self.assertRaises(m.IsolationError):
            m.fixed_exclusion_regex(['Test' + 'x' * 600])
        with self.assertRaises(m.IsolationError):
            m.fixed_exclusion_regex(['TestA', 'TestA'])

    def run_fixture(self, process_codes):
        record = {'package': m.SERVICE_PACKAGE, 'directory': 'backend/internal/service',
                  'binary': 'binaries/service.test', 'binary_sha256': 'a' * 64}
        compiled = {'package_count': 1, 'packages': [record]}
        listed = SimpleNamespace(returncode=0, stdout=self.listing(), stderr='initialization diagnostic')
        main_listing = '\n'.join(n for n in self.listing().splitlines() if n not in m.FIXED_SERVICE_TESTS)
        selected = SimpleNamespace(returncode=0, stdout=main_listing, stderr='regex diagnostic')
        execute = Mock(side_effect=[listed, selected, *[SimpleNamespace(returncode=code) for code in process_codes]])
        doc = {'source_commit': 'b' * 40, 'source_receipt_sha256': 'c' * 64, 'files': {}}
        env = m.test_environment()
        with patch.object(m, 'cleanup_test_caches', return_value=[]), patch.object(m, 'verify_compiled', return_value=(compiled, 'd' * 64)), contextlib.redirect_stdout(io.StringIO()):
            receipt = m.execute_suite(Path('/src'), doc, env, 'e' * 64, 'unit', execute)
        return receipt, execute, env

    def test_schedule_identical_before_success_or_failure_and_no_retries(self):
        green, green_calls, _ = self.run_fixture([0, 0, 0, 0, 0])
        red, red_calls, _ = self.run_fixture([1, 0, 1, 0, 0])
        self.assertEqual(green['test_commands'], red['test_commands'])
        self.assertEqual(green_calls.call_count, 7)
        self.assertEqual(red_calls.call_count, 7)
        self.assertEqual(green['test_exit_code'], 0)
        self.assertEqual(len(green['child_process_resources']), 7)
        for observation in green['child_process_resources']:
            self.assertIn('memory.events', observation['before']['cgroup_v2'])
            self.assertIn('memory.peak', observation['after']['cgroup_v2'])
            self.assertNotIn('ru_maxrss', observation['reaped_children_usage_delta'])
        self.assertEqual(green['service_partition_audit']['go_main_regex_verification']['status'], 'passed')
        self.assertEqual(red['test_exit_code'], 1)
        self.assertEqual([p['exit_code'] for p in red['package_results']], [1, 0, 1, 0, 0])
        self.assertEqual([p['partition'] for p in red['package_results']], ['main', *m.FIXED_SERVICE_TESTS])
        for call in red_calls.call_args_list:
            self.assertEqual(call.kwargs['cwd'], Path('/src/backend/internal/service'))
            self.assertIn('/opt/isolation/compiled/binaries/service.test', call.args[0])
        for call in red_calls.call_args_list[2:]:
            self.assertIn('-test.timeout=20m', call.args[0])
            self.assertIn('-test.count=1', call.args[0])
            self.assertEqual(sum(arg.startswith('-test.run=') for arg in call.args[0]), 1)

    def test_synthetic_public_client_id_is_service_child_only_before_listing_init(self):
        receipt, calls, env = self.run_fixture([0, 0, 0, 0, 0])
        self.assertNotIn('GEMINI_CLI_OAUTH_CLIENT_ID', env)
        self.assertEqual(receipt['synthetic_service_child_environment'], {'GEMINI_CLI_OAUTH_CLIENT_ID': 'custom-test-client'})
        for call in calls.call_args_list:
            self.assertEqual(call.kwargs['env']['GEMINI_CLI_OAUTH_CLIENT_ID'], 'custom-test-client')
            self.assertEqual(call.kwargs['env']['GOPROXY'], 'off')
            self.assertNotIn('GEMINI_CLI_OAUTH_CLIENT_SECRET', call.kwargs['env'])
        self.assertEqual(m.package_environment({'package': 'example/other'}, env), env)
        with self.assertRaises(m.IsolationError):
            m.check_environment({'GEMINI_CLI_OAUTH_CLIENT_ID': 'custom-test-client'})

    def test_go_regex_inventory_mismatch_stops_before_tests(self):
        record = {'package': m.SERVICE_PACKAGE, 'directory': 'backend/internal/service',
                  'binary': 'binaries/service.test', 'binary_sha256': 'a' * 64}
        doc = {'source_commit': 'b' * 40, 'source_receipt_sha256': 'c' * 64, 'files': {}}
        main = '\n'.join(n for n in self.listing().splitlines() if n not in m.FIXED_SERVICE_TESTS)
        variants = [(0, main.replace('ExampleFeature', '')), (0, main + '\nTestExtra'),
                    (0, main + '\nExample'), (0, main.replace('BenchmarkNotRun', '')),
                    (0, main + '\nBenchmarkExtra'), (0, main + '\nunknown output'), (2, main)]
        for code, output in variants:
            execute = Mock(side_effect=[SimpleNamespace(returncode=0, stdout=self.listing(), stderr=''),
                                       SimpleNamespace(returncode=code, stdout=output, stderr='fixture diagnostic')])
            captured = io.StringIO()
            with self.subTest(code=code, output=output[-30:]), patch.object(m, 'verify_compiled', return_value=({'package_count': 1, 'packages': [record]}, 'd' * 64)), patch.object(m, 'cleanup_test_caches', return_value=[]), contextlib.redirect_stdout(captured), self.assertRaises(m.IsolationError):
                m.execute_suite(Path('/src'), doc, m.test_environment(), 'e' * 64, 'unit', execute)
            self.assertEqual(execute.call_count, 2)
            self.assertIn('"record_type": "go_main_regex_verification"', captured.getvalue())
            self.assertIn('"status": "failed"', captured.getvalue())
            for call in execute.call_args_list:
                self.assertTrue(any(arg.startswith('-test.list=') for arg in call.args[0]))

    def test_inventory_error_prevents_all_partition_execution(self):
        record = {'package': m.SERVICE_PACKAGE, 'directory': 'backend/internal/service',
                  'binary': 'binaries/service.test', 'binary_sha256': 'a' * 64}
        execute = Mock(return_value=SimpleNamespace(returncode=0, stdout='TestMissingFixed\n', stderr=''))
        doc = {'source_commit': 'b' * 40, 'source_receipt_sha256': 'c' * 64, 'files': {}}
        with patch.object(m, 'cleanup_test_caches', return_value=[]), patch.object(m, 'verify_compiled', return_value=({'package_count': 1, 'packages': [record]}, 'd' * 64)), contextlib.redirect_stdout(io.StringIO()), self.assertRaises(m.IsolationError):
            m.execute_suite(Path('/src'), doc, m.test_environment(), 'e' * 64, 'unit', execute)
        self.assertEqual(execute.call_count, 1)
        self.assertIn('-test.list=.', execute.call_args.args[0])


if __name__ == '__main__':
    unittest.main()
