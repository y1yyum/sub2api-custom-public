#!/usr/bin/env python3
"""Static pinned-fixture checks only. No Go or runtime/network operations."""
import hashlib
import json
from pathlib import Path
import runpy
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[2]
MANIFEST = json.loads((ROOT / '.github/upgrade/manifest.json').read_text())
ADAPT = runpy.run_path(str(ROOT / '.github/upgrade/adapt-service-race-fixtures.py'))['adapt']
PATHS = ['backend/internal/service/'+name for name in (
    'channel_monitor_checker_body_test.go', 'grok_free_quota_gate_test.go',
    'usage_cleanup_service_test.go', 'auth_service_email_bind_test.go')]


def normalize(text):
    for new, old in (
        ('h.snapshot().', 'h.'), ('repo.callCount()', 'repo.calls'),
        ('repo.queriedIDs()', 'repo.lastIDs'), ('repo.queryStart()', 'repo.start'),
        ('dashboardRepo.recomputeCallCount()', 'dashboardRepo.recomputeCalls'),
        ('cache.sentEmails()', 'cache.setEmails'),
    ):
        text = text.replace(new, old)
    return text


class ServiceRaceFixtureChecks(unittest.TestCase):
    def test_pins_original_assertions_test_counts_and_no_new_skips_or_sleeps(self):
        for path in PATHS:
            for revision, expected in MANIFEST['test_adaptations'][path].items():
                with self.subTest(path=path, revision=revision):
                    raw = subprocess.check_output(['git', '-C', str(ROOT), 'show', revision+':'+path])
                    self.assertEqual(hashlib.sha256(raw).hexdigest(), expected)
                    before = raw.decode()
                    after = ADAPT(path, before)
                    self.assertEqual(before.count('func Test'), after.count('func Test'))
                    self.assertEqual(before.count('t.Skip('), after.count('t.Skip('))
                    self.assertEqual(before.count('time.Sleep('), after.count('time.Sleep('))
                    for line in before.splitlines():
                        if any(marker in line for marker in ('require.', 't.Errorf(', 't.Fatalf(')):
                            self.assertIn(line, normalize(after))

    def test_worker_lifecycle_uses_production_completion_markers(self):
        path = PATHS[1]
        before = subprocess.check_output(['git', '-C', str(ROOT), 'show', MANIFEST['target']+':'+path]).decode()
        after = ADAPT(path, before)
        self.assertNotIn('openaiGrokFreeQuotaGateCache = sync.Map{}', after)
        self.assertNotIn('m.Delete(int64(1))', after)
        self.assertIn('freeQuotaRefreshInFlight.Load(cache)', after)
        self.assertIn('cache.Clear()', after)
        self.assertIn('fixture refresh must finish before reset/teardown', after)

    def test_email_get_returns_value_copy(self):
        path = PATHS[3]
        before = subprocess.check_output(['git', '-C', str(ROOT), 'show', MANIFEST['target']+':'+path]).decode()
        after = ADAPT(path, before)
        self.assertNotIn('return s.data, nil', after)
        self.assertIn('copyData := *s.data\n\treturn &copyData, nil', after)


if __name__ == '__main__':
    unittest.main(verbosity=2)
