#!/usr/bin/env python3
"""Source-level custom protections; not a substitute for runtime tests."""
import hashlib
import json
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]
MANIFEST = json.loads((ROOT / '.github/upgrade/manifest.json').read_text())


class CustomPreservation(unittest.TestCase):
    def test_custom_paths_retained(self):
        self.assertEqual(len(MANIFEST['preserved_paths']), 108)
        for path in MANIFEST['preserved_paths']:
            with self.subTest(path=path):
                self.assertTrue((ROOT / path).is_file())

    def test_published_migrations_and_release_identity_unchanged(self):
        for path, expected in MANIFEST['immutable_sha256'].items():
            with self.subTest(path=path):
                self.assertEqual(hashlib.sha256((ROOT / path).read_bytes()).hexdigest(), expected)

    def test_excluded_cli_packages_remain_absent(self):
        for package in MANIFEST['absent_production_packages']:
            for path in (ROOT / package).glob('*.go'):
                self.assertTrue(path.name.endswith('_test.go'), str(path))

    def test_entrypoint_executable(self):
        self.assertTrue((ROOT / 'deploy/docker-entrypoint.sh').stat().st_mode & 0o111)

    def test_unified_settings_fields_retained(self):
        text = (ROOT / 'backend/internal/service/settings_view.go').read_text()
        self.assertEqual(text.count('json:"unified_model_channel_status_enabled"'), 2)
        for lang in ('en', 'zh'):
            text = (ROOT / f'frontend/src/i18n/locales/{lang}/admin/settings.ts').read_text()
            self.assertIn('unifiedStatus:', text)
            self.assertIn('unifiedStatusHint:', text)

    def test_oauth_has_no_builtin_fallback(self):
        text = (ROOT / 'backend/internal/pkg/antigravity/oauth.go').read_text()
        self.assertRegex(text, r'(?m)^var ClientID string$')
        self.assertRegex(text, r'(?m)^var defaultClientSecret string$')
        self.assertIn('os.Getenv(AntigravityOAuthClientIDEnv)', text)
        self.assertIn('os.Getenv(AntigravityOAuthClientSecretEnv)', text)


if __name__ == '__main__':
    unittest.main(verbosity=2)
