"""Regression tests for the local PR job selection."""
import runpy
import unittest
from pathlib import Path


class FullUnitScopeTest(unittest.TestCase):
    def test_unit_runner_change_selects_full_frontend(self):
        script = Path(__file__).resolve().parents[1] / ".gitea" / "ci-scope.py"
        scope = runpy.run_path(str(script))["scope"]
        jobs = scope([".gitea/unit-full.sh"], "HEAD")
        self.assertEqual(jobs["unit"], "./...")
        self.assertTrue(jobs["frontend"])
