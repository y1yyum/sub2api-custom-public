"""Exact single-fixture ordering proof, with no Go or network execution."""
import hashlib
import json
from pathlib import Path
import re
import runpy
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[2]
FIXTURE = runpy.run_path(str(ROOT / '.github/upgrade/adapt-pool-deadline-fixture.py'))


class PoolDeadlineFixture(unittest.TestCase):
    def test_exact_source_reverse_transform_and_original_contract(self):
        manifest = json.loads((ROOT / '.github/upgrade/manifest.json').read_text())
        path = FIXTURE['PATH']
        raw = subprocess.check_output(['git', '-C', str(ROOT), 'show', manifest['target'] + ':' + path])
        self.assertEqual(hashlib.sha256(raw).hexdigest(), manifest['test_adaptations'][path][manifest['target']])
        before = raw.decode()
        after = runpy.run_path(str(ROOT / '.github/upgrade/adapt-tests.py'))['adapt'](path, before)
        self.assertEqual(after.count(FIXTURE['HELPER']), 1)
        restored = after.replace(FIXTURE['HELPER'], '', 1).replace(FIXTURE['AFTER'], FIXTURE['BEFORE'], 1).replace(FIXTURE['CONSTRUCTOR_AFTER'], FIXTURE['CONSTRUCTOR_BEFORE'], 1)
        self.assertEqual(restored, before, 'all other fixtures, mock methods and assertions must remain byte-identical')
        for token in ('func Test', 'require.', 't.Parallel()', 't.Skip(', 'time.Sleep('):
            self.assertEqual(before.count(token), after.count(token))
        pattern = r'(?ms)^func TestTokenRefreshService_LateSuccessPastAttemptDeadlineIsRejected\(t \*testing.T\) \{.*?^}\n'
        test = re.search(pattern, after)[0]
        self.assertIn('svc.attemptTimeoutOverride = 10 * time.Millisecond', test)
        self.assertIn('require.ErrorAs(t, err, &timeoutErr)', test)
        self.assertIn('require.Empty(t, updatedIDs, "credentials returned after the deadline must not be persisted")', test)
        self.assertIn('require.Zero(t, setErrorCalls)', test)
        self.assertIn('require.Equal(t, 1, setTempUnschedCalls)', test)
        self.assertIn('<-ctx.Done()', FIXTURE['HELPER'])
        self.assertIn('return r.poolHealthRefresher.Refresh(ctx, account)', FIXTURE['HELPER'])
        self.assertEqual(after.count('&poolHealthLateSuccessRefresher{}'), 1)
        # The constructor takes a concrete *poolHealthRefresher, not an interface.
        # Its registration stores the embedded mock; this direct-attempt fixture
        # must pass the context-ordering wrapper to refreshWithRetry itself.
        self.assertIn('func newPoolHealthService(repo *poolHealthAccountRepo, refresher *poolHealthRefresher,', after)
        self.assertEqual(test.count(FIXTURE['CONSTRUCTOR_AFTER']), 1)
        self.assertNotIn(FIXTURE['CONSTRUCTOR_BEFORE'], test)
        self.assertIn('err := svc.refreshWithRetry(context.Background(), &account, refresher, nil, time.Hour)', test)

    def test_changed_setup_fails_closed(self):
        raw = subprocess.check_output(['git', '-C', str(ROOT), 'show',
            'dece26153621959ccd7635814f4b2d08304e681a:' + FIXTURE['PATH']], text=True)
        with self.assertRaises(ValueError):
            FIXTURE['adapt'](FIXTURE['PATH'], raw.replace(FIXTURE['BEFORE'], '\trefresher := changed\n', 1))
        with self.assertRaises(ValueError):
            FIXTURE['adapt']('backend/unreviewed_test.go', raw)
        start = raw.index(FIXTURE['TARGET'])
        changed = raw[:start] + raw[start:].replace(FIXTURE['CONSTRUCTOR_BEFORE'], 'changedConstructor()', 1)
        with self.assertRaises(ValueError):
            FIXTURE['adapt'](FIXTURE['PATH'], changed)


if __name__ == '__main__':
    unittest.main()
