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
    'backend/internal/securityaudit/prompt_config_integration_test.go',
    'backend/internal/securityaudit/prompt_payload_store_integration_test.go',
)


def without_redis_lifecycle(text):
    # Normalize only the reviewed endpoint/client setup, not any business assertion.
    text = re.sub(r'\t(?:redisAddress|address) := strings.TrimSpace\(os.Getenv\(promptAuditRedisTestEnv\)\)\n\tif (?:redisAddress|address) == "" \{\n\t\tt.Skip\(promptAuditRedisTestEnv \+ " is not set"\)\n\t}\n', '', text)
    text = re.sub(r'\t(?:redisClient|client) := redis.NewClient\(&redis.Options\{Addr: (?:redisAddress|address)}\)\n\tt.Cleanup\(func\(\) \{ require.NoError\(t, (?:redisClient|client).Close\(\)\) }\)', '', text)
    text = re.sub(r'\t(?:redisClient|client), stopRedis, err := isolatedfixture.StartRedis\(context.Background\(\)\)\n\trequire.NoError\(t, err, "isolated prompt-audit Redis fixture must be available"\)\n\tt.Cleanup\(func\(\) \{ require.NoError\(t, stopRedis\(\), "isolated prompt-audit Redis cleanup"\) }\)', '', text)
    return text


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
                    self.assertEqual(re.findall(pattern, without_redis_lifecycle(before)),
                                     re.findall(pattern, without_redis_lifecycle(after)))
                    self.assertNotIn('testcontainers', after)
                    self.assertNotIn('DOCKER_HOST', after)
                    self.assertNotIn('t.Skip(', after)

    def test_support_is_pinned_and_does_not_accept_operator_endpoints(self):
        for destination, spec in MANIFEST['validation_support'].items():
            self.assertIn(destination, {'backend/internal/isolatedfixture/fixture.go',
                                        'backend/internal/securityaudit/isolated_shared_fixture_test.go'})
            data = (ROOT / spec['source']).read_bytes()
            self.assertEqual(hashlib.sha256(data).hexdigest(), spec['sha256'])
            text = data.decode()
            if destination.endswith('isolated_shared_fixture_test.go'):
                self.assertNotIn('//go:build', text)
                continue
            self.assertIn('Network: "unix"', text)
            self.assertIn('"--port", "0"', text)
            self.assertIn('exec.Command("/usr/local/bin/redis-server"', text)
            self.assertNotIn('PROMPT_AUDIT_TEST_POSTGRES_DSN', text)
            self.assertIn('sub2api_repository', text)
            self.assertIn('sub2api_prompt_audit', text)
            self.assertIn('127.0.0.1:55432', text)

    def test_prompt_reclassification_is_explicit(self):
        records = MANIFEST['test_reclassifications']
        self.assertEqual(len(records), 3)
        self.assertEqual([r['count'] for r in records], [6, 1, 2])
        self.assertEqual(sum(r['count'] for r in records), 9)
        for record in records:
            upstream = subprocess.check_output(['git', '-C', str(ROOT), 'show',
                MANIFEST['target'] + ':' + record['path']]).decode()
            self.assertEqual(record['test_functions'], re.findall(r'^func (Test\w+)\(', upstream, re.M))
            self.assertEqual(len(set(record['test_functions'])), record['count'])
            self.assertTrue(ADAPT(record['path'], upstream).startswith('//go:build integration\n'))

    def test_shared_helpers_retained_verbatim_and_untagged(self):
        shared = (ROOT / '.github/upgrade/fixtures/isolated_shared_fixture_test.go').read_text()
        for path, name in [(PATHS[3], 'integrationResult'), (PATHS[4], 'promptAuditUpdateRequest')]:
            pattern = r'(?ms)^func ' + name + r'\(.*?^}\n'
            for revision in MANIFEST['test_adaptations'][path]:
                before = subprocess.check_output(['git', '-C', str(ROOT), 'show', revision + ':' + path]).decode()
                helper = re.search(pattern, before).group(0)
                self.assertEqual(shared.count(helper), 1)
                self.assertNotIn('func ' + name + '(', ADAPT(path, before))
        self.assertNotIn('//go:build', shared)

    def test_shared_helper_allowlist_is_one_exact_securityaudit_path(self):
        for script in ['prepare-upgrade-validation.py', 'run-isolated-unit.py']:
            allow = runpy.run_path(str(ROOT / '.github/scripts' / script))['allowed_support_path']
            self.assertTrue(allow('backend/internal/securityaudit/isolated_shared_fixture_test.go'))
            for path in ['backend/internal/securityaudit/arbitrary.go',
                         'backend/internal/securityaudit/isolated_shared_fixture_test.go/extra',
                         'backend/internal/securityaudit/../service/file.go',
                         'backend/internal/isolatedfixture/../service/file.go']:
                self.assertFalse(allow(path), path)


if __name__ == '__main__':
    unittest.main(verbosity=2)
