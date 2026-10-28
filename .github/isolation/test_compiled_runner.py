"""Mock-only compiler/runner contract tests. No Go, Docker or network executes."""
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

SCRIPTS = Path(__file__).resolve().parents[1] / 'scripts'
spec = importlib.util.spec_from_file_location('compiler', SCRIPTS / 'compile-isolated-tests.py')
c = importlib.util.module_from_spec(spec)
spec.loader.exec_module(c)
m = c.runtime


class CompiledContracts(unittest.TestCase):
    def fixture(self, directory, support=None):
        root = Path(directory) / 'src'
        (root / 'backend/plain').mkdir(parents=True)
        (root / 'backend/go.mod').write_text('module example.invalid/app\n\ngo 1.27.3\n')
        (root / 'backend/main_test.go').write_text('package app\n')
        (root / 'backend/plain/main.go').write_text('package plain\n')
        catalog = root / 'frontend/src/constants/platformCatalog.builtin.json'
        catalog.parent.mkdir(parents=True)
        catalog.write_text('{}\n')
        hashes = {p.relative_to(root).as_posix(): m.file_digest(p) for p in root.rglob('*') if p.is_file()}
        receipt = {'tested_commit': 'a' * 40, 'working_tree_diff': '', 'source_sha256': hashes}
        if support:
            target = root / support
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text('package securityaudit\n')
            sha = m.file_digest(target)
            receipt['source_sha256']['.github/upgrade/fixtures/shared_test.go'] = sha
            receipt['validation_support'] = [{'destination': support, 'source': '.github/upgrade/fixtures/shared_test.go', 'sha256': sha}]
        (root / 'validation-receipt.json').write_text(json.dumps(receipt))
        doc = {'schema': 1, 'source_commit': 'a' * 40, 'source_receipt_sha256': m.file_digest(root / 'validation-receipt.json'),
               'files': {p.relative_to(root).as_posix(): m.file_digest(p) for p in root.rglob('*') if p.is_file()}}
        manifest = Path(directory) / 'source-manifest.json'
        manifest.write_text(json.dumps(doc))
        output = Path(directory) / 'compiled'
        return root, manifest, m.file_digest(manifest), output, doc

    def fake_compiler(self, root, calls, fail=False):
        def execute(command, **kwargs):
            calls.append((command, kwargs))
            self.assertEqual(kwargs['env']['GOPROXY'], 'off')
            self.assertEqual(kwargs['env']['GOTOOLCHAIN'], 'local')
            if command == [m.GO, 'version']:
                return SimpleNamespace(returncode=0, stdout='go version go1.27.3 linux/amd64\n')
            if command[1] == 'list':
                packages = [{'ImportPath': 'example.invalid/app', 'Dir': str(root / 'backend'), 'TestGoFiles': ['main_test.go']},
                            {'ImportPath': 'example.invalid/app/plain', 'Dir': str(root / 'backend/plain')}]
                return SimpleNamespace(returncode=0, stdout='\n'.join(map(json.dumps, packages)))
            self.assertEqual(command[:3], [m.GO, 'test', '-c'])
            self.assertIn('-race', command)
            self.assertIn('-mod=readonly', command)
            self.assertNotIn('-exec', command)
            if fail:
                return SimpleNamespace(returncode=1)
            if kwargs['cwd'] == root / 'backend':
                binary = Path(command[command.index('-o') + 1])
                binary.write_bytes(b'fixture-not-an-executable')
                binary.chmod(0o755)
            return SimpleNamespace(returncode=0)
        return execute

    def compile_fixture(self, directory):
        root, manifest, sha, output, doc = self.fixture(directory)
        calls = []
        with contextlib.redirect_stdout(io.StringIO()):
            c.build(root, manifest, sha, output, 'unit', inspect=lambda *_: doc,
                    execute=self.fake_compiler(root, calls))
        return root, manifest, sha, output, doc, calls

    def test_compile_covers_all_packages_without_executing_binaries(self):
        with tempfile.TemporaryDirectory() as directory:
            root, _, sha, output, doc, calls = self.compile_fixture(directory)
            self.assertEqual(len(calls), 4)  # version, list, both packages compiled
            for command, kwargs in calls[1:]:
                self.assertIn('-race', command)
                self.assertIn('-tags=unit', command)
                self.assertIn('-mod=readonly', command)
            with patch.object(m, 'COMPILED_ROOT', output):
                receipt, _ = m.verify_compiled(root, doc, sha, 'unit', output)
            self.assertEqual(receipt['package_count'], 2)
            self.assertFalse(receipt['test_binaries_executed'])
            self.assertEqual([r['compile_status'] for r in receipt['packages']], ['compiled', 'no_test_files'])

    def test_preflight_failure_never_starts_go(self):
        execute = Mock()
        def denied(*_):
            raise m.IsolationError('fixture denial')
        with self.assertRaises(m.IsolationError):
            c.build(Path('/src'), Path('/manifest'), '0' * 64, Path('/output'), 'unit', inspect=denied, execute=execute)
        execute.assert_not_called()

    def test_compile_failure_never_writes_success_receipt(self):
        with tempfile.TemporaryDirectory() as directory:
            root, manifest, sha, output, doc = self.fixture(directory)
            with self.assertRaises(m.IsolationError):
                c.build(root, manifest, sha, output, 'unit', inspect=lambda *_: doc,
                        execute=self.fake_compiler(root, [], fail=True))
            self.assertFalse((output / 'compile-receipt.json').exists())

    def test_missing_corrupt_or_unsafe_binary_fails_before_execution(self):
        for change in ('missing', 'corrupt', 'writable', 'symlink', 'extra'):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as directory:
                root, _, sha, output, doc, _ = self.compile_fixture(directory)
                binary = next((output / 'binaries').iterdir())
                if change == 'missing': binary.unlink()
                elif change == 'corrupt': binary.write_bytes(b'changed')
                elif change == 'writable': binary.chmod(0o777)
                elif change == 'extra': (output / 'unlisted').write_text('extra')
                else:
                    binary.unlink()
                    binary.symlink_to(root / 'backend/go.mod')
                execute = Mock()
                original = m.verify_compiled
                with patch.object(m, 'COMPILED_ROOT', output), patch.object(m, 'verify_compiled', side_effect=lambda *_: original(root, doc, sha, 'unit', output)), self.assertRaises(m.IsolationError):
                    m.execute_suite(root, doc, m.test_environment(), sha, 'unit', execute)
                execute.assert_not_called()

    def test_wrong_source_suite_or_missing_package_is_rejected(self):
        for change in ('source', 'suite', 'package'):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as directory:
                root, _, sha, output, doc, _ = self.compile_fixture(directory)
                p = output / 'compile-receipt.json'
                receipt = json.loads(p.read_text())
                if change == 'source': receipt['source_manifest_sha256'] = 'b' * 64
                elif change == 'suite': receipt['suite'] = 'integration'
                else: receipt['packages'].pop()
                p.write_text(json.dumps(receipt))
                with patch.object(m, 'COMPILED_ROOT', output), self.assertRaises(m.IsolationError):
                    m.verify_compiled(root, doc, sha, 'unit', output)

    def test_every_binary_runs_even_after_failure_in_correct_directory(self):
        records = [{'package': m.SERVICE_PACKAGE, 'directory': 'backend/a', 'binary': 'binaries/a.test', 'binary_sha256': 'a' * 64},
                   {'package': 'example/b', 'directory': 'backend/b', 'binary': 'binaries/b.test', 'binary_sha256': 'b' * 64},
                   {'package': 'example/c', 'directory': 'backend/c', 'binary': None}]
        listing = 'TestNormal\n' + m.FIXED_SERVICE_TESTS[1] + '\n'
        execute = Mock(side_effect=[SimpleNamespace(returncode=0, stdout=listing, stderr=''),
                                   SimpleNamespace(returncode=0, stdout='TestNormal\n', stderr=''),
                                   SimpleNamespace(returncode=1), SimpleNamespace(returncode=0), SimpleNamespace(returncode=0)])
        doc = {'source_commit': 'a' * 40, 'source_receipt_sha256': 'b' * 64, 'files': {}}
        with patch.object(m, 'verify_compiled', return_value=({'package_count': 3, 'packages': records}, 'c' * 64)), contextlib.redirect_stdout(io.StringIO()):
            result = m.execute_suite(Path('/src'), doc, m.test_environment(), 'd' * 64, 'integration', execute)
        self.assertEqual(result['test_exit_code'], 1)
        self.assertEqual(execute.call_count, 5)
        self.assertEqual([call.kwargs['cwd'] for call in execute.call_args_list], [Path('/src/backend/a')] * 4 + [Path('/src/backend/b')])
        for call in execute.call_args_list[2:]:
            self.assertEqual(call.args[0][:3], [m.GO, 'tool', 'test2json'])
            self.assertIn('-test.v=test2json', call.args[0])
            self.assertIn('-test.count=1', call.args[0])
            self.assertIn('-test.timeout=20m', call.args[0])
        self.assertEqual(result['package_results'][0]['status'], 'no_test_files')

    def test_securityaudit_support_exact_path_and_hash_only(self):
        for allowed in (True, False):
            with tempfile.TemporaryDirectory() as directory:
                destination = 'backend/internal/securityaudit/' + ('isolated_shared_fixture_test.go' if allowed else 'arbitrary_test.go')
                root, manifest, sha, _, _ = self.fixture(directory, support=destination)
                if allowed:
                    m.verify_sources(root, manifest, sha)
                else:
                    with self.assertRaises(m.IsolationError): m.verify_sources(root, manifest, sha)


if __name__ == '__main__':
    unittest.main()
