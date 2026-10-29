"""Source-only verification of the pinned async slog capture repair."""
import hashlib
import json
from pathlib import Path
import runpy
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[2]
PATH = 'backend/internal/service/content_moderation_test.go'
FIXTURE = runpy.run_path(str(ROOT / '.github/upgrade/adapt-service-race-fixtures.py'))


class ModerationLogFixture(unittest.TestCase):
    def test_exact_reverse_transformation_and_assertion_counts(self):
        manifest = json.loads((ROOT / '.github/upgrade/manifest.json').read_text())
        raw = subprocess.check_output(['git', '-C', str(ROOT), 'show', manifest['target']+':'+PATH])
        self.assertEqual(hashlib.sha256(raw).hexdigest(), manifest['test_adaptations'][PATH][manifest['target']])
        before = raw.decode()
        after = runpy.run_path(str(ROOT / '.github/upgrade/adapt-tests.py'))['adapt'](PATH, before)
        helper = FIXTURE['CONTENT_MODERATION_LOG_BUFFER']
        self.assertTrue(after.endswith(helper))
        restored = after[:-len(helper)].replace(
            'var slogOutput contentModerationLockedLogBuffer', 'var slogOutput bytes.Buffer', 1)
        self.assertEqual(restored, before)  # Whole source, not just selected assertions.
        for token in ('func Test', 'require.', 't.Parallel()', 't.Skip(', 'time.Sleep('):
            self.assertEqual(after.count(token), before.count(token))
        self.assertEqual(after.count('var slogOutput contentModerationLockedLogBuffer'), 1)
        self.assertEqual(helper.count('b.mu.Lock()'), 2)
        self.assertEqual(helper.count('defer b.mu.Unlock()'), 2)
        self.assertIn('return b.buffer.Write(p)', helper)
        self.assertIn('return b.buffer.String()', helper)
        self.assertNotIn('.Bytes()', helper)


if __name__ == '__main__':
    unittest.main()
