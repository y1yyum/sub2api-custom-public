"""Static proof of the exact late-success mock ordering repair; no Go execution."""
import hashlib
import json
from pathlib import Path
import re
import runpy
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[2]
FIXTURE = runpy.run_path(str(ROOT / '.github/upgrade/adapt-oauth-deadline-fixture.py'))


class OAuthDeadlineFixture(unittest.TestCase):
    def test_full_source_reverse_transform_and_unchanged_assertions(self):
        manifest = json.loads((ROOT / '.github/upgrade/manifest.json').read_text())
        path = FIXTURE['PATH']
        raw = subprocess.check_output(['git', '-C', str(ROOT), 'show', manifest['target']+':'+path])
        self.assertEqual(hashlib.sha256(raw).hexdigest(),manifest['test_adaptations'][path][manifest['target']])
        before = raw.decode()
        after = runpy.run_path(str(ROOT / '.github/upgrade/adapt-tests.py'))['adapt'](path,before)
        name = 'TestRefreshIfNeeded_LateSuccessAfterDeadlineDoesNotPersist'
        pattern = r'(?ms)^func '+name+r'\(t \*testing.T\) \{.*?^}\n'
        oldtest = re.search(pattern,before)[0]
        newtest = re.search(pattern,after)[0]
        restored_test = newtest.replace('\texecutor.onRefresh = func() { <-ctx.Done() }\n','',1)
        restored_test = restored_test.replace('\t\tcredentials:  map[string]any{"access_token": "late-token"},\n',
            '\t\tcredentials:  map[string]any{"access_token": "late-token"},\n\t\tdelay:        30 * time.Millisecond,\n',1)
        self.assertEqual(restored_test,oldtest)
        self.assertEqual(after.replace(newtest,restored_test,1),before)
        for token in ('func Test', 'require.', 't.Parallel()', 't.Skip(', 'time.Sleep('):
            self.assertEqual(before.count(token),after.count(token))
        self.assertEqual(after.count('executor.onRefresh = func() { <-ctx.Done() }'),1)
        self.assertIn('context.WithTimeout(context.Background(), 10*time.Millisecond)',newtest)
        self.assertIn('require.ErrorIs(t, err, context.DeadlineExceeded)',newtest)
        self.assertIn('require.Nil(t, result)',newtest)
        self.assertIn('require.Zero(t, repo.updateCredentialsCalls,',newtest)


if __name__ == '__main__':
    unittest.main()
