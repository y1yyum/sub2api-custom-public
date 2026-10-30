"""The old mocked loopback cadence fixture explicitly opts into private probes."""
import hashlib
import json
from pathlib import Path
import runpy
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[2]


class ProbePrivateFixture(unittest.TestCase):
    def test_only_configuration_is_added_and_all_assertions_are_retained(self):
        manifest = json.loads((ROOT / '.github/upgrade/manifest.json').read_text())
        path = 'backend/internal/service/upstream_billing_probe_test.go'
        raw = subprocess.check_output(['git', '-C', str(ROOT), 'show', manifest['target'] + ':' + path])
        self.assertEqual(hashlib.sha256(raw).hexdigest(), manifest['test_adaptations'][path][manifest['target']])
        text = raw.decode()
        adapted = runpy.run_path(str(ROOT / '.github/upgrade/adapt-tests.py'))['adapt'](path, text)
        added = '\tcfg.Security.AccountProbe.AllowPrivateHosts = true\n'
        self.assertEqual(adapted.count(added), 1)
        self.assertEqual(adapted.replace(added, ''), text)
        self.assertIn('http://127.0.0.1:8080', text)
        self.assertNotIn('t.Skip(', adapted[len(text):])


if __name__ == '__main__':
    unittest.main()
