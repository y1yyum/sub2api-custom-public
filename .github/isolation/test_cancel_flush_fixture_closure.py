"""Bare source vet excludes regression fixtures; both isolated suites restore helpers."""
import pathlib
import subprocess
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]


class CancelFlushFixtureClosure(unittest.TestCase):
    def test_owned_fixture_is_both_suite_tagged_and_helpers_are_pinned(self):
        owned = (ROOT / 'backend/internal/service/openai_custom_cancel_flush_contract_test.go').read_text()
        self.assertTrue(owned.startswith('//go:build unit || integration\n\n'))
        helper = subprocess.check_output([
            'git', '-C', str(ROOT), 'show',
            'dece26153621959ccd7635814f4b2d08304e681a:backend/internal/service/openai_gateway_response_flush_test.go',
        ], text=True)
        for name in ('newOpenAIResponseFlushRecorder', 'runOpenAIResponseFlushTest'):
            self.assertIn('func ' + name + '(', helper)
            self.assertIn(name + '(', owned)


if __name__ == '__main__':
    unittest.main()
