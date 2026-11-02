"""Offline negative checks for the static inventory validator."""
import importlib.util
import json
from pathlib import Path
import shutil
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location('contracts_validator', Path(__file__).with_name('validate_contracts.py'))
VALIDATOR = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VALIDATOR)


class ContractInventoryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.manifest = self.root / 'docs/integration/contracts.json'
        self.doc = json.loads((ROOT / 'docs/integration/contracts.json').read_text())
        paths = {'docs/integration/contracts.json', self.doc['historical_source']['path'],
                 self.doc['current_ci']['workflow'], self.doc['current_ci']['critical_list']}
        for item in self.doc['contracts']:
            paths.update(p for p in (item['current_path'], item['fixture_path']) if p)
            paths.update(f['path'] for f in item['additional_fixture_inputs'])
        for path in paths:
            target = self.root / path
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(ROOT / path, target)

    def check(self):
        with patch.object(VALIDATOR, 'ROOT', self.root), patch('builtins.print'):
            VALIDATOR.validate()

    def save(self):
        self.manifest.write_text(json.dumps(self.doc))

    def test_valid_inventory(self):
        self.check()

    def test_missing_contract_rejected(self):
        self.doc['contracts'].pop()
        self.save()
        with self.assertRaisesRegex(ValueError, 'identity/order'):
            self.check()

    def test_source_change_rejected(self):
        path = self.root / self.doc['contracts'][-1]['current_path']
        path.write_text(path.read_text() + '\n// changed fixture\n')
        with self.assertRaisesRegex(ValueError, 'changed input'):
            self.check()

    def test_removed_restored_test_rejected(self):
        test = self.doc['contracts'][0]
        path = self.root / test['current_path']
        path.write_text(path.read_text().replace('func ' + test['test'] + '(',
                                               'func RemovedContract('))
        with self.assertRaisesRegex(ValueError, 'presence changed'):
            self.check()

    def test_fixture_change_rejected(self):
        path = self.root / self.doc['contracts'][26]['fixture_path']
        path.write_text(path.read_text() + '\n// changed harness\n')
        with self.assertRaisesRegex(ValueError, 'changed input'):
            self.check()

    def test_lane_mismatch_rejected(self):
        self.doc['contracts'][9]['lane'] = 'integration'
        self.save()
        with self.assertRaisesRegex(ValueError, 'build lane mismatch'):
            self.check()

    def test_summary_change_rejected(self):
        self.doc['coverage']['missing'] = 26
        self.save()
        with self.assertRaisesRegex(ValueError, 'coverage summary'):
            self.check()

    def test_critical_list_change_rejected(self):
        path = self.root / self.doc['current_ci']['critical_list']
        path.write_text(path.read_text() + '\n# changed list\n')
        with self.assertRaisesRegex(ValueError, 'changed input'):
            self.check()

    def test_skip_weakening_rejected(self):
        self.doc['contracts'][0]['allowed_skips'] = ['missing fixture']
        self.save()
        with self.assertRaisesRegex(ValueError, 'acceptance policy'):
            self.check()

    def test_inventory_cannot_claim_runtime_pass(self):
        self.doc['contracts'][0]['execution_status'] = 'PASS'
        self.save()
        with self.assertRaisesRegex(ValueError, 'not runtime evidence'):
            self.check()

    def test_inventory_cannot_release_gate(self):
        self.doc['gate_status'] = 'PASS'
        self.save()
        with self.assertRaisesRegex(ValueError, 'cannot release G0'):
            self.check()


if __name__ == '__main__':
    unittest.main()
