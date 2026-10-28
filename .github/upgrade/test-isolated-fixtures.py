#!/usr/bin/env python3
"""Static fixture transformation tests. Never executes Go, Redis or PostgreSQL."""
import hashlib
import json
from pathlib import Path
import re
import runpy
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[2]
MANIFEST = json.loads((ROOT / '.github/upgrade/manifest.json').read_text())
ADAPT = runpy.run_path(str(ROOT / '.github/upgrade/adapt-integration-fixtures.py'))['adapt']
PATHS = (
    'backend/internal/repository/integration_harness_test.go',
    'backend/internal/middleware/rate_limiter_integration_test.go',
    'backend/internal/server/routes/auth_rate_limit_integration_test.go',
    'backend/internal/securityaudit/prompt_repository_integration_test.go',
)


class IsolatedFixtureStaticTests(unittest.TestCase):
    def test_pinned_inputs_and_unchanged_test_bodies(self):
        for path in PATHS:
            for revision, digest in MANIFEST['test_adaptations'][path].items():
                with self.subTest(path=path, revision=revision):
                    raw = subprocess.check_output(['git', '-C', str(ROOT), 'show', revision+':'+path])
                    self.assertEqual(hashlib.sha256(raw).hexdigest(), digest)
                    before = raw.decode()
                    after = ADAPT(path, before)
                    pattern = r'(?ms)^func (Test(?!Main)\w*\(.*?)(?=^func |\Z)'
                    self.assertEqual(re.findall(pattern, before), re.findall(pattern, after))
                    self.assertNotIn('testcontainers', after)
                    self.assertNotIn('DOCKER_HOST', after)
                    self.assertNotIn('t.Skip(', after)

    def test_support_is_pinned_and_does_not_accept_operator_endpoints(self):
        for destination, spec in MANIFEST['validation_support'].items():
            self.assertEqual(destination, 'backend/internal/isolatedfixture/fixture.go')
            data = (ROOT / spec['source']).read_bytes()
            self.assertEqual(hashlib.sha256(data).hexdigest(), spec['sha256'])
            text = data.decode()
            self.assertIn('Network: "unix"', text)
            self.assertIn('"--port", "0"', text)
            self.assertIn('exec.Command("/usr/local/bin/redis-server"', text)
            self.assertNotIn('PROMPT_AUDIT_TEST_POSTGRES_DSN', text)
            self.assertIn('sub2api_repository', text)
            self.assertIn('sub2api_prompt_audit', text)
            self.assertIn('127.0.0.1:55432', text)

    def test_prompt_reclassification_is_explicit(self):
        records = MANIFEST['test_reclassifications']
        self.assertEqual(len(records), 1)
        self.assertEqual(records[0]['path'], PATHS[-1])
        self.assertEqual(records[0]['count'], 6)
        self.assertEqual(len(set(records[0]['test_functions'])), 6)


if __name__ == '__main__':
    unittest.main(verbosity=2)
