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

    def test_route_fixture_preserves_all_assertions_and_requires_guard_order(self):
        manifest = json.loads((ROOT / '.github/upgrade/manifest.json').read_text())
        path = ADAPTER['ROUTE_PATH']
        raw = subprocess.check_output(['git', '-C', str(ROOT), 'show', manifest['target'] + ':' + path])
        self.assertEqual(hashlib.sha256(raw).hexdigest(), manifest['test_adaptations'][path][manifest['target']])
        original = raw.decode()
        adapted = ADAPTER['adapt'](path, original)
        restored = adapted
        for old, (new, count) in ADAPTER['ROUTE_REPLACEMENTS'].items():
            self.assertEqual(adapted.count(new), count)
            restored = restored.replace(new, old)
        self.assertEqual(restored, original)
        self.assertEqual(adapted.count('modelUniqueness, groupModelAllowlist'), 6)
        self.assertEqual(adapted.count('require.'), original.count('require.'))
        self.assertEqual(adapted.count('func Test'), original.count('func Test'))
        with self.assertRaises(ValueError):
            ADAPTER['adapt'](path, original.replace('gateway.Use(groupModelAllowlist)', 'changed'))

    def test_ws_owned_regression_requires_overlay_but_runs_in_both_lanes(self):
        path = ROOT / 'backend/internal/handler/openai_ws_model_uniqueness_test.go'
        source = path.read_text()
        self.assertTrue(source.startswith('//go:build unit || integration\n\npackage handler\n'))
        self.assertIn('func TestOpenAIWSModelMultiplicityWithoutAllowlist(', source)
        manifest = json.loads((ROOT / '.github/upgrade/manifest.json').read_text())
        for path, symbols in {
            'backend/internal/handler/openai_gateway_handler_test.go': [
                'func runOpenAIResponsesWebSocketUsageLogCase(',
                'type openAIResponsesWSUsageLogCase struct'],
            'backend/internal/handler/openai_gateway_ws_model_allowlist_test.go': [
                'func wsAllowlistGroup('],
        }.items():
            restored = subprocess.check_output([
                'git', '-C', str(ROOT), 'show', manifest['target'] + ':' + path
            ]).decode()
            self.assertNotIn('//go:build', restored)
            for symbol in symbols:
                self.assertIn(symbol, restored)
        # The other three owned tests use only production declarations and
        # standard/imported test utilities; they remain available to bare vet.
        for path in [
            'backend/internal/pkg/requestmodel/model_uniqueness_test.go',
            'backend/internal/service/gateway_model_uniqueness_test.go',
            'backend/internal/server/middleware/gateway_model_uniqueness_test.go',
        ]:
            self.assertNotIn('//go:build', (ROOT / path).read_text())

    def test_every_http_chain_has_guard_before_allowlist(self):
        source = (ROOT / 'backend/internal/server/routes/gateway.go').read_text()
        lines = [line for line in source.splitlines() if 'groupModelAllowlist)' in line or 'groupModelAllowlist,' in line]
        self.assertEqual(len(lines), 6)
        self.assertTrue(all('modelUniqueness, groupModelAllowlist' in line for line in lines))


if __name__ == '__main__':
    unittest.main()
