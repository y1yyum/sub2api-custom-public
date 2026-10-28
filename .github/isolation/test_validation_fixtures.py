"""Static/pure fixture checks only: no Go, Docker, DNS or test binary runs."""
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import runpy
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
ADAPT = runpy.run_path(str(ROOT / '.github/upgrade/adapt-validation-fixtures.py'))['adapt']
MANIFEST = json.loads((ROOT / '.github/upgrade/manifest.json').read_text())
spec = importlib.util.spec_from_file_location('runtime_fixture', ROOT / '.github/scripts/test-isolated-unit-entrypoint.py')
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)
m = fixture.m
PUBLIC = 'frontend/src/constants/platformCatalog.builtin.json'


class HermeticValidationFixtures(unittest.TestCase):
    def test_pinned_matrix_inputs_preserve_original_assertions_and_no_external_hostnames(self):
        path = 'backend/internal/service/channel_monitor_quota_mode_test.go'
        for revision, expected in MANIFEST['test_adaptations'][path].items():
            with self.subTest(revision=revision):
                raw = subprocess.check_output(['git', '-C', str(ROOT), 'show', revision + ':' + path])
                self.assertEqual(hashlib.sha256(raw).hexdigest(), expected)
                before = raw.decode()
                after = ADAPT(path, before)
                start = after.index('func TestValidateCreateParams_CheckModeMatrix(')
                end = after.find('\nfunc ', start + 1)
                matrix = after[start:] if end == -1 else after[start:end]
                self.assertEqual(matrix.count('https://192.0.2.1'), 5)
                self.assertNotIn('https://api.', matrix)
                self.assertNotIn('https://example.com', matrix)
                self.assertIn('wantErr: ErrChannelMonitorEndpointPrivate', matrix)
                assertions = lambda s: [line.strip() for line in s.splitlines() if 'require.' in line or 'assert.' in line]
                self.assertEqual(assertions(before), assertions(after))
                self.assertNotIn('t.Skip(', after)

    def test_public_catalog_is_the_only_pinned_frontend_input(self):
        self.assertEqual(set(MANIFEST['public_test_inputs']), {PUBLIC})
        self.assertEqual(hashlib.sha256((ROOT / PUBLIC).read_bytes()).hexdigest(), MANIFEST['public_test_inputs'][PUBLIC])
        for name in ('unit.Dockerfile', 'integration.Dockerfile'):
            dockerfile = (ROOT / '.github/isolation' / name).read_text()
            lines = [line for line in dockerfile.splitlines() if line.startswith('COPY frontend')]
            self.assertEqual(lines, ['COPY ' + PUBLIC + ' /src/' + PUBLIC])

    def test_runtime_rejects_missing_tampered_or_extra_frontend_files(self):
        for change in ('missing', 'tampered', 'extra'):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as directory:
                root, manifest, sha = fixture.RuntimeFixtures().source_fixture(directory)
                if change == 'missing':
                    (root / PUBLIC).unlink()
                elif change == 'tampered':
                    (root / PUBLIC).write_text('{"changed":true}')
                else:
                    extra = root / 'frontend/unreviewed.ts'
                    extra.write_text('unreviewed')
                    doc = json.loads(manifest.read_text())
                    doc['files']['frontend/unreviewed.ts'] = m.digest(extra.read_bytes())
                    manifest.write_text(json.dumps(doc))
                    sha = m.digest(manifest.read_bytes())
                with self.assertRaises(m.IsolationError):
                    m.verify_sources(root, manifest, sha)

    def test_original_platform_sync_assertions_remain_and_file_is_present(self):
        raw = subprocess.check_output(['git', '-C', str(ROOT), 'show', MANIFEST['target'] + ':backend/internal/service/platform_catalog_test.go']).decode()
        self.assertIn('require.JSONEq(t, string(want), string(got)', raw)
        self.assertNotIn('backend/internal/service/platform_catalog_test.go', MANIFEST['test_adaptations'])
        self.assertTrue((ROOT / PUBLIC).is_file())


if __name__ == '__main__':
    unittest.main()
