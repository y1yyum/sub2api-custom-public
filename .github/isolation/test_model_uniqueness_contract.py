"""Pure pinned adaptation/wiring proof; never invokes Go or tests outside isolation."""
import hashlib
import json
from pathlib import Path
import runpy
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[2]
ADAPTER = runpy.run_path(str(ROOT / '.github/upgrade/adapt-model-uniqueness.py'))


class ModelUniquenessContract(unittest.TestCase):
    def test_only_intentional_same_value_contract_changes(self):
        manifest = json.loads((ROOT / '.github/upgrade/manifest.json').read_text())
        path = ADAPTER['PATH']
        raw = subprocess.check_output(['git', '-C', str(ROOT), 'show', manifest['target']+':'+path])
        self.assertEqual(hashlib.sha256(raw).hexdigest(), manifest['test_adaptations'][path][manifest['target']])
        original = raw.decode()
        result = ADAPTER['adapt'](path, original)
        restored = result.replace(ADAPTER['NEW'], ADAPTER['OLD']).replace(', closeReason: \"duplicate model field\",', ',')
        self.assertEqual(restored, original)
        self.assertEqual(result.count('func Test'), original.count('func Test'))
        self.assertNotIn('t.Skip', ADAPTER['NEW'])
        self.assertIn('firstFrameCloseExpected: true', ADAPTER['NEW'])
        self.assertIn('OpenAIWSIngressModeDedicated', ADAPTER['NEW'])

    def test_every_http_chain_has_guard_before_allowlist(self):
        source = (ROOT / 'backend/internal/server/routes/gateway.go').read_text()
        lines = [line for line in source.splitlines() if 'groupModelAllowlist)' in line or 'groupModelAllowlist,' in line]
        self.assertEqual(len(lines), 6)
        self.assertTrue(all('modelUniqueness, groupModelAllowlist' in line for line in lines))


if __name__ == '__main__':
    unittest.main()
