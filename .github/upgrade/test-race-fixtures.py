#!/usr/bin/env python3
"""Static adapter checks only: reads pinned Git source; never executes Go."""
import hashlib
import json
from pathlib import Path
import runpy
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[2]
MANIFEST = json.loads((ROOT / '.github/upgrade/manifest.json').read_text())
ADAPT = runpy.run_path(str(ROOT / '.github/upgrade/adapt-race-fixtures.py'))['adapt']
PATHS = (
    'backend/internal/handler/admin/grok_import_probe_test.go',
    'backend/internal/handler/admin/grok_oauth_handler_test.go',
    'backend/ent/schema/auth_identity_schema_test.go',
)


class RaceFixtureStaticTests(unittest.TestCase):
    def test_pins_assertions_and_test_counts(self):
        for path in PATHS:
            for revision, expected in MANIFEST['test_adaptations'][path].items():
                with self.subTest(path=path, revision=revision):
                    data = subprocess.check_output(['git', '-C', str(ROOT), 'show', revision+':'+path])
                    self.assertEqual(hashlib.sha256(data).hexdigest(), expected)
                    before = data.decode()
                    after = ADAPT(path, before)
                    self.assertEqual(before.count('func Test'), after.count('func Test'))
                    normalized = after.replace('repo.snapshotUpdates(42)', 'repo.updates[42]')
                    for line in before.splitlines():
                        if 'require.' in line:
                            self.assertIn(line, normalized)
                    self.assertEqual(before.count('t.Skip('), after.count('t.Skip('))
                    self.assertEqual(before.count('time.Sleep('), after.count('time.Sleep('))

    def test_locked_buffer_returns_copy(self):
        path = PATHS[0]
        data = subprocess.check_output(['git', '-C', str(ROOT), 'show', MANIFEST['target']+':'+path]).decode()
        after = ADAPT(path, data)
        self.assertIn('return append([]byte(nil), b.buffer.Bytes()...)', after)
        self.assertEqual(after.count('defer b.mu.Unlock()'), 3)
        self.assertIn('require.NotContains(t, logs.String(), "refresh-token-secret")', after)

    def test_schema_load_remains_exact_and_source_not_writable(self):
        path = PATHS[2]
        data = subprocess.check_output(['git', '-C', str(ROOT), 'show', MANIFEST['target']+':'+path]).decode()
        after = ADAPT(path, data)
        self.assertIn('(&load.Config{Path: "."}).Load()', after)
        self.assertIn('scratch := t.TempDir()', after)
        self.assertIn('t.Chdir(filepath.Join(scratch, "ent", "schema"))', after)
        self.assertIn('if !info.Mode().IsRegular()', after)
        self.assertNotIn('os.Chmod', after)


if __name__ == '__main__':
    unittest.main(verbosity=2)
