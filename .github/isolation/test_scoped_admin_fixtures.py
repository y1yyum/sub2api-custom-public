"""Static closure proof: 17 mandatory DB tests and eight retained pure unit tests."""
import hashlib
import json
from pathlib import Path
import re
import runpy
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[2]
DIR = ROOT / '.github/upgrade'
MANIFEST = json.loads((DIR / 'manifest.json').read_text())
ADAPTER = runpy.run_path(str(DIR / 'adapt-scoped-admin-fixtures.py'))
SPEC = ADAPTER['SPEC']


def source(path):
    return subprocess.check_output(['git', '-C', str(ROOT), 'show', MANIFEST['target']+':'+path]).decode()


class ScopedAdminFixtures(unittest.TestCase):
    def test_exact_closure_and_mandatory_database(self):
        records = {r['path']:r for r in MANIFEST['test_reclassifications'] if 'SCOPED_ADMIN_TEST_DSN' in r['from']}
        self.assertEqual(len(SPEC['paths']), 11)
        self.assertEqual(sum(r['count'] for r in records.values()), 17)
        for path in SPEC['paths']:
            with self.subTest(path=path):
                before = source(path)
                self.assertEqual(hashlib.sha256(before.encode()).hexdigest(), MANIFEST['test_adaptations'][path][MANIFEST['target']])
                after = ADAPTER['adapt'](path,before)
                self.assertTrue(after.startswith('//go:build integration\n'))
                self.assertNotIn('t.Skip(', after)
                self.assertNotIn('SCOPED_ADMIN_TEST_DSN', after)
                self.assertEqual(re.findall(r'^func (Test\w+)\(', before, re.M), re.findall(r'^func (Test\w+)\(', after, re.M))
                expected = [name for name in re.findall(r'^func (Test\w+)\(', before, re.M) if name not in SPEC['unit_copies'].get(path, [])]
                self.assertEqual(records.get(path, {}).get('test_functions', []), expected)
                # Every original assertion remains, including transactions and rollback.
                for line in before.splitlines():
                    if any(s in line for s in ('require.', 't.Fatal(', 't.Fatalf(', 't.Errorf(')):
                        self.assertIn(line,after)
                if 'SCOPED_ADMIN_TEST_DSN' in before:
                    self.assertIn('isolatedfixture.PostgresDSN("repository")', after)
                    self.assertIn('isolated schema cleanup', after)
                    self.assertIn('dsn+"&search_path="+', after)

    def test_eight_unit_test_copies_are_verbatim_and_unique(self):
        total = 0
        for path,names in SPEC['unit_copies'].items():
            package = path.split('/')[2]
            destination = f'backend/internal/{package}/isolated_scoped_admin_unit_test.go'
            support = MANIFEST['validation_support'][destination]
            raw = (ROOT / support['source']).read_bytes()
            self.assertEqual(hashlib.sha256(raw).hexdigest(),support['sha256'])
            copied = raw.decode()
            self.assertTrue(copied.startswith('//go:build unit\n'))
            for name in names:
                fn = re.search(r'(?ms)^func '+name+r'\(t \*testing.T\) \{.*?^}\n',source(path)).group(0)
                self.assertIn(fn,copied)
                self.assertEqual(copied.count('func '+name+'('),1)
                total += 1
        self.assertEqual(total,8)


if __name__ == '__main__':
    unittest.main()
