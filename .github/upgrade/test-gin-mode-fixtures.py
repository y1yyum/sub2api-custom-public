#!/usr/bin/env python3
"""Source-only tests; never invokes Go, Docker, or application code."""
import hashlib
import json
from pathlib import Path
import re
import runpy
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[2]
ADAPTER = runpy.run_path(str(ROOT / '.github/upgrade/adapt-gin-mode-fixtures.py'))
MANIFEST = json.loads((ROOT / '.github/upgrade/manifest.json').read_text())


class GinModeFixtureTests(unittest.TestCase):
    def test_pins_and_exact_non_setup_content(self):
        for path in [*ADAPTER['COUNTS'], ADAPTER['HELPER']]:
            for revision, digest in MANIFEST['test_adaptations'][path].items():
                with self.subTest(path=path, revision=revision):
                    raw = subprocess.check_output(['git', '-C', str(ROOT), 'show', revision+':'+path])
                    self.assertEqual(hashlib.sha256(raw).hexdigest(), digest)
                    before = raw.decode()
                    after = ADAPTER['adapt'](path, before)
                    for marker in ('func Test', '.Parallel()', 't.Skip(', 'time.Sleep('):
                        self.assertEqual(before.count(marker), after.count(marker))
                    if path == ADAPTER['HELPER']:
                        after = re.sub(r'// Establish the helper.*?^}\n\n', '', after, count=1, flags=re.S | re.M)
                    def without_setup(text):
                        return '\n'.join(line for line in text.splitlines() if line.strip() and 'gin.SetMode(gin.TestMode)' not in line)
                    # Every statement/assertion except mode setup is byte-identical.
                    self.assertEqual(without_setup(before), without_setup(after))
                    if path != ADAPTER['HELPER']:
                        moved = 0
                        for fn in re.findall(r'(?ms)^func Test\w+\(t \*testing.T\) \{.*?(?=^func |\Z)', after):
                            if '\n\tt.Parallel()\n' not in fn or 'gin.SetMode' not in fn:
                                continue
                            self.assertEqual(fn.count('gin.SetMode'), 1)
                            self.assertLess(fn.index('gin.SetMode'), fn.index('t.Parallel()'))
                            moved += 1
                        self.assertEqual(moved, ADAPTER['COUNTS'][path])
                    else:
                        helper = after.split('func newTestContext()', 1)[1].split('\n}', 1)[0]
                        self.assertNotIn('gin.SetMode', helper)


if __name__ == '__main__':
    unittest.main(verbosity=2)
