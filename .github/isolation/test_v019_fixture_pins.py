"""Source/mock-only guards for the v0.2.19 fixture transition."""
import hashlib
import json
from pathlib import Path
import runpy
import subprocess
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
BASE = '01842576b3d5c1e2545b5a99ad555d36e28ed306'
PREVIOUS = 'ac1b8f1271be0d674fa6f074403c1410f30a3a81'
TARGET = 'dece26153621959ccd7635814f4b2d08304e681a'
MANIFEST = json.loads((ROOT / '.github/upgrade/manifest.json').read_text())


class FixtureRevisionTransition(unittest.TestCase):
    def test_exact_historical_and_target_fetch_closure(self):
        fetch = runpy.run_path(str(ROOT / '.github/scripts/fetch-upgrade-fixture-inputs.py'))
        self.assertEqual(fetch['PINNED'], (BASE, PREVIOUS, TARGET))
        self.assertEqual((MANIFEST['content_base'], MANIFEST['upgrade_base'], MANIFEST['target']), fetch['PINNED'])
        self.assertEqual(MANIFEST['test_revision'], TARGET)
        self.assertEqual(MANIFEST['test_overrides'], {})
        changed = dict(MANIFEST, target='0' * 40)
        with patch('subprocess.run') as run, patch('subprocess.check_output') as output:
            with self.assertRaisesRegex(RuntimeError, 'reviewed fixed'):
                fetch['fetch_inputs'](ROOT, changed)
            run.assert_not_called()
            output.assert_not_called()

    def test_new_target_adaptations_and_reviewed_deltas(self):
        adapt = runpy.run_path(str(ROOT / '.github/upgrade/adapt-tests.py'))['adapt']
        changed = []
        for path, pins in MANIFEST['test_adaptations'].items():
            with self.subTest(path=path):
                self.assertIn(TARGET, pins)
                if PREVIOUS in pins:
                    self.assertIn(BASE, pins)
                raw = subprocess.check_output(['git', '-C', str(ROOT), 'show', TARGET+':'+path])
                self.assertEqual(hashlib.sha256(raw).hexdigest(), pins[TARGET])
                after = adapt(path, raw.decode())
                if PREVIOUS in pins and pins[TARGET] != pins[PREVIOUS]:
                    changed.append(path)
                if path == 'backend/internal/server/api_contract_test.go':
                    self.assertEqual(after.count('"image_count_billing": false,'), 1)
                    self.assertEqual(after.count('"unified_model_channel_status_enabled": false,'), 2)
                if path == 'backend/internal/service/account_test_service_gemini_test.go':
                    marker = 'func TestGeminiDefaultTestModel(t *testing.T) {'
                    self.assertEqual(after[after.index(marker):], raw.decode()[raw.decode().index(marker):])
        self.assertEqual(sorted(changed), [
            'backend/internal/server/api_contract_test.go',
            'backend/internal/service/account_test_service_gemini_test.go',
        ])

    def test_stale_scope_production_gate_remains_exact(self):
        gate = runpy.run_path(str(ROOT / '.github/upgrade/adapt-stale-scope-fixture.py'))
        upstream = [subprocess.check_output(['git', '-C', str(ROOT), 'show', rev+':'+gate['PRODUCTION_PATH']])
                    for rev in (PREVIOUS, TARGET)]
        self.assertEqual(upstream[0], upstream[1])
        # The pinned production hash is the custom source, not pristine upstream.
        gate['verify_production']((ROOT / gate['PRODUCTION_PATH']).read_bytes())


if __name__ == '__main__':
    unittest.main()
