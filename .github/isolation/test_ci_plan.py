"""快速范围、完整分片和实际执行证据的边界回归；不运行应用代码。"""
from collections import Counter
import importlib.util
import os
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, ROOT / '.github/scripts' / path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


planner = load('ci_planner', 'ci-plan.py')
contract = load('ci_plan_contract', 'test-plan.py')
SHA = 'a' * 40
PKG = contract.MODULE + 'internal/payment'


class CoveragePlan(unittest.TestCase):
    def plan(self, mode='fast'):
        return {'mode': mode, 'source_commit': SHA, 'suite': 'unit', 'shards': 4,
                'packages': {PKG: ['TestMoney']} if mode == 'fast' else None, 'slow_tests': {}}

    def evidence(self):
        plan = self.plan()
        record = {'package': PKG, 'binary': 'money.test', 'binary_sha256': 'b' * 64}
        part = {'name': 'main', 'tests': ['TestMoney'], 'regex': '^(TestMoney)$', 'go_regex_verified': True}
        receipt = {'suite': 'unit', 'source_commit': SHA,
                   'planned_package_audits': [{'package': PKG, 'binary_sha256': 'b' * 64,
                       'discovery_exit_code': 0, 'registry': ['TestMoney', 'TestOther'],
                       'selected': ['TestMoney'], 'partitions': [part]}],
                   'package_results': [{'package': PKG, 'partition': 'main',
                       'binary_sha256': 'b' * 64, 'exit_code': 0}]}
        events = Counter({(PKG, 'TestMoney', 'run'): 1, (PKG, 'TestMoney', 'pass'): 1})
        return plan, receipt, events, {'packages': [record]}

    def test_fast_selection_reports_its_unselected_coverage(self):
        plan, receipt, events, compiled = self.evidence()
        result = contract.audit(receipt, events, plan, compiled)
        self.assertEqual(result['selected_top_level_tests'], 1)
        self.assertEqual(result['unselected_top_level_tests'], 1)
        self.assertEqual(result['mode'], 'fast')

    def test_missing_duplicate_or_unplanned_execution_fails(self):
        for name, count in [('TestMoney', 0), ('TestMoney', 2), ('TestOther', 1)]:
            with self.subTest(name=name, count=count):
                plan, receipt, events, compiled = self.evidence()
                events[(PKG, name, 'run')] = count
                with self.assertRaises(ValueError):
                    contract.audit(receipt, events, plan, compiled)

    def test_failed_partition_wrong_hash_or_missing_exit_fails(self):
        for change in ['failure', 'hash', 'missing', 'regex']:
            with self.subTest(change=change):
                plan, receipt, events, compiled = self.evidence()
                if change == 'failure': receipt['package_results'][0]['exit_code'] = 1
                if change == 'hash': receipt['package_results'][0]['binary_sha256'] = 'c' * 64
                if change == 'missing': receipt['package_results'] = []
                if change == 'regex': receipt['planned_package_audits'][0]['partitions'][0]['go_regex_verified'] = False
                with self.assertRaises(ValueError):
                    contract.audit(receipt, events, plan, compiled)

    def test_full_shards_are_disjoint_and_cover_renamed_slow_tests(self):
        plan = self.plan('full')
        names = ['TestFast', 'TestSlowA', 'TestSlowB', 'TestRenamed']
        plan['slow_tests'] = {PKG: ['TestSlowA', 'TestSlowB', 'TestOldName']}
        selected, parts = contract.memberships(plan, PKG, names)
        self.assertEqual(Counter(n for p in parts for n in p['tests']), Counter(names))
        self.assertEqual(selected, sorted(names))
        self.assertIn('TestRenamed', parts[0]['tests'])
        self.assertNotIn('TestOldName', selected)

    def test_service_global_state_and_allocation_remain_separate(self):
        plan = self.plan('full')
        names = [*contract.FIXED['unit'], 'TestFast', 'TestSlow']
        plan['slow_tests'] = {contract.SERVICE: ['TestSlow', contract.FIXED['unit'][0]]}
        _, parts = contract.memberships(plan, contract.SERVICE, names)
        for name in contract.FIXED['unit']:
            self.assertIn({'name': name, 'tests': [name]}, parts)
        with self.assertRaises(ValueError):
            contract.memberships(plan, contract.SERVICE, names[1:])

    def test_package_escape_empty_selection_and_source_mismatch_fail(self):
        for change in ['escape', 'empty', 'source', 'full_subset', 'shards']:
            with self.subTest(change=change):
                plan = self.plan()
                if change == 'escape': plan['packages'] = {contract.MODULE + '../escape': ['TestA']}
                if change == 'empty': plan['packages'] = {}
                if change == 'source': plan['source_commit'] = 'd' * 40
                if change == 'full_subset': plan['mode'] = 'full'
                if change == 'shards': plan['shards'] = 100
                with self.assertRaises(ValueError):
                    contract.validate(plan, 'unit', SHA)
        self.assertEqual(contract.package_args(self.plan()), [PKG])
        self.assertEqual(contract.package_args(self.plan('full')), ['./...'])


class ScopeSelection(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.old = Path.cwd()
        os.chdir(self.temp.name)
        self.add('.github/ci/slow-unit-tests.txt', '')
        self.add('.github/ci/critical-tests.txt', 'backend/internal/payment/money_test.go\n')
        self.add('.github/ci/frontend-critical-tests.txt', 'src/auth.spec.ts\n')
        self.add('.github/upgrade/manifest.json', '{"required_integration_tests":[]}')
        self.add('backend/internal/payment/money.go', 'package payment\n')
        self.add('backend/internal/payment/money_test.go', 'package payment\nfunc TestMoney(t *testing.T) {}\n')
        self.add('frontend/src/auth.spec.ts', '')
        self.add('frontend/src/widget.ts', '')
        self.add('frontend/src/widget.spec.ts', '')

    def tearDown(self):
        os.chdir(self.old)
        self.temp.cleanup()

    def add(self, name, text):
        path = Path(name)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding='utf-8')

    def test_docs_skip_heavy_jobs_but_the_plan_is_explicit(self):
        plan = planner.make_plan(['docs/example.md'], 'fast', SHA)
        self.assertEqual(plan['suites']['unit']['packages'], {})
        self.assertFalse(plan['frontend'])
        self.assertFalse(plan['image'])

    def test_module_patch_keeps_fast_critical_regressions(self):
        plan = planner.make_plan(['backend/go.mod'], 'fast', SHA)
        self.assertEqual(plan['mode'], 'fast')
        self.assertEqual(plan['suites']['unit']['packages'][PKG], ['TestMoney'])
        self.assertFalse(plan['image'])

    def test_frontend_change_runs_critical_and_corresponding_tests(self):
        plan = planner.make_plan(['frontend/src/widget.ts'], 'fast', SHA)
        self.assertEqual(plan['frontend_tests'], ['src/auth.spec.ts', 'src/widget.spec.ts'])

    def test_full_has_no_package_or_test_subset(self):
        plan = planner.make_plan([], 'full', SHA)
        self.assertTrue(plan['image'])
        self.assertTrue(plan['frontend'])
        for suite in ('unit', 'integration'):
            self.assertIsNone(plan['suites'][suite]['packages'])
            contract.validate(plan['suites'][suite], suite, SHA)


if __name__ == '__main__':
    unittest.main()
