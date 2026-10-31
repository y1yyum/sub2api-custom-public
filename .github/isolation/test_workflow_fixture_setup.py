"""Static guard: every isolation job must fetch pinned inputs before checks."""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[2]


class FixtureSetupOrdering(unittest.TestCase):
    def test_both_isolated_lanes_fetch_before_any_python_checks(self):
        workflow = (ROOT / '.github/workflows/ci.yml').read_text()
        for job in ('changes', 'tests'):
            with self.subTest(job=job):
                block = re.search(r'(?ms)^  '+re.escape(job)+r':\n(.*?)(?=^  [a-z][a-z-]*:|\Z)', workflow).group(1)
                self.assertEqual(block.count('python3 .github/scripts/fetch-upgrade-fixture-inputs.py'), 1)
                commands = [line.strip() for line in block.splitlines() if 'python3 ' in line]
                self.assertEqual(commands[0], 'run: python3 .github/scripts/fetch-upgrade-fixture-inputs.py')
                self.assertLess(block.index('fetch-upgrade-fixture-inputs.py'), block.index('-m unittest'))
        self.assertIn("suite: " + chr(36) + "{{ fromJSON(needs.changes.outputs.suites) }}", workflow)
        planner = (ROOT / '.github/scripts/ci-plan.py').read_text()
        self.assertIn("SUITES = ('unit', 'integration')", planner)

    def test_source_preparation_reuses_the_same_pinned_fetch(self):
        prepare = (ROOT / '.github/scripts/prepare-upgrade-validation.py').read_text()
        self.assertIn("fetch-upgrade-fixture-inputs.py'))['fetch_inputs'](ROOT, manifest)", prepare)
        self.assertIn("revisions = {manifest['test_revision'], *manifest['test_overrides'].values()}", prepare)
        self.assertIn("if not revisions <= {manifest['content_base'], manifest['target']}", prepare)
        helper = (ROOT / '.github/scripts/fetch-upgrade-fixture-inputs.py').read_text()
        self.assertIn("'https://github.com/Wei-Shaw/sub2api.git'", helper)
        self.assertIn("'--no-tags', '--depth=1'", helper)
        self.assertIn("actual != revision", helper)


if __name__ == '__main__':
    unittest.main()
