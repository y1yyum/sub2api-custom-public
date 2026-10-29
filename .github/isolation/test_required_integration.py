"""Pure fail-closed coverage tests; never executes Go or databases."""
from collections import Counter
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('required_host', Path(__file__).parents[1] / 'scripts/run-isolated-unit.py')
host = importlib.util.module_from_spec(spec)
spec.loader.exec_module(host)


class RequiredIntegration(unittest.TestCase):
    def test_mandatory_test_never_missing_skipped_duplicated_or_failed(self):
        identity = ('github.com/Wei-Shaw/sub2api/internal/repository', 'TestMandatory')
        good = Counter({(*identity, 'run'): 1, (*identity, 'pass'): 1})
        self.assertEqual(host.audit_required_integration([identity], good, 'integration')['status'], 'passed')
        for action, count in [('run', 0), ('run', 2), ('pass', 0), ('pass', 2), ('skip', 1), ('fail', 1)]:
            with self.subTest(action=action, count=count):
                bad = good.copy(); bad[(*identity, action)] = count
                self.assertEqual(host.audit_required_integration([identity], bad, 'integration')['status'], 'failed')
        self.assertEqual(host.audit_required_integration([], Counter(), 'integration')['status'], 'failed')

    def test_manifest_derivation_and_duplicate_or_traversal_rejection(self):
        item = {'path': 'backend/internal/repository/example_test.go', 'test_functions': ['TestMandatory']}
        doc = {'test_reclassifications': [item]}
        self.assertEqual(host.required_integration_tests(doc), [('github.com/Wei-Shaw/sub2api/internal/repository', 'TestMandatory')])
        for malformed in [{'test_reclassifications': [item, item]}, {'test_reclassifications': [dict(item, path='backend/internal/../elsewhere.go')]}]:
            with self.assertRaises(RuntimeError): host.required_integration_tests(malformed)
        self.assertEqual(host.audit_required_integration([], Counter(), 'unit')['status'], 'not_applicable')

    def test_startup_package_exception_is_exact_and_cannot_skip(self):
        package = 'github.com/Wei-Shaw/sub2api/cmd/server'
        name = 'TestCustomValidateAutoSetupStartupRejectsBeforeWrites'
        doc = {'required_integration_tests': [{'package': package, 'test': name}]}
        self.assertEqual(host.required_integration_tests(doc), [(package, name)])
        for invalid in [package + '/nested', package + 'extra', package + '/../other',
                        'github.com/Wei-Shaw/sub2api/cmd/other',
                        'github.com/Wei-Shaw/sub2api/internal/../cmd/server']:
            with self.subTest(package=invalid), self.assertRaises(RuntimeError):
                host.required_integration_tests({'required_integration_tests': [
                    {'package': invalid, 'test': name}]})
        self.assertEqual(host.audit_required_integration([(package, name)],
            Counter({(package, name, 'run'): 1, (package, name, 'skip'): 1}),
            'integration')['status'], 'failed')


if __name__ == '__main__':
    unittest.main()
