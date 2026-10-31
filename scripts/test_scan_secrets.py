import contextlib
import importlib.util
import io
import json
import shlex
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock


SCRIPT = Path(__file__).with_name("scan-secrets.py")
spec = importlib.util.spec_from_file_location("scan_secrets", SCRIPT)
scanner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(scanner)


class ScanSecretsTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.report = self.root / "raw.json"

    def finding(self):
        return {
            "RuleID": "generic-api-key",
            "File": "config.txt",
            "StartLine": 3,
            "Commit": "a" * 40,
            "Fingerprint": "a" * 40 + ":config.txt:generic-api-key:3",
            "Secret": "sensitive-test-value",
            "Match": "token=sensitive-test-value",
            "Author": "private author",
            "Email": "private@example.invalid",
            "Message": "private commit message",
        }

    def write_report(self, data):
        self.report.write_text(json.dumps(data), encoding="utf-8")

    def test_empty_success_is_valid(self):
        self.write_report([])
        self.assertEqual(scanner.load_findings(self.report, 0), [])

    def test_findings_only_keep_location_metadata(self):
        self.write_report([self.finding()])
        result = scanner.load_findings(self.report, 10)
        self.assertEqual(result[0]["File"], "config.txt")
        self.assertNotIn("sensitive-test-value", json.dumps(result))
        for field in ("Secret", "Match", "Author", "Email", "Message"):
            self.assertNotIn(field, result[0])

    def test_missing_report_is_error(self):
        with self.assertRaises(scanner.ScanError):
            scanner.load_findings(self.report, 0)

    def test_invalid_json_is_error_without_echoing_content(self):
        self.report.write_text("sensitive-test-value", encoding="utf-8")
        with self.assertRaises(scanner.ScanError) as error:
            scanner.load_findings(self.report, 0)
        self.assertNotIn("sensitive-test-value", str(error.exception))

    def test_invalid_report_shapes_are_errors(self):
        for data in (None, {}, {"error": "failed"}, [None], [{}]):
            with self.subTest(data=data):
                self.write_report(data)
                with self.assertRaises(scanner.ScanError):
                    scanner.load_findings(self.report, 10)

    def test_invalid_location_fields_are_errors(self):
        for field, value in (("File", ""), ("RuleID", 42), ("StartLine", 0),
                             ("StartLine", True), ("Commit", [])):
            with self.subTest(field=field, value=value):
                finding = self.finding()
                finding[field] = value
                self.write_report([finding])
                with self.assertRaises(scanner.ScanError):
                    scanner.load_findings(self.report, 10)

    def test_scanner_failure_cannot_pass_with_empty_report(self):
        self.write_report([])
        for status in (1, 2, -9, 124):
            with self.subTest(status=status):
                with self.assertRaises(scanner.ScanError):
                    scanner.load_findings(self.report, status)

    def test_exit_status_and_findings_must_agree(self):
        for data, status in (([], 10), ([self.finding()], 0)):
            with self.subTest(status=status):
                self.write_report(data)
                with self.assertRaises(scanner.ScanError):
                    scanner.load_findings(self.report, status)

    def test_strict_findings_block_but_daily_findings_do_not(self):
        scans = [{"scope": "history", "findings": [self.finding()]}]
        self.assertEqual(scanner.result_code(scans, strict=False), 0)
        self.assertEqual(scanner.result_code(scans, strict=True), 1)
        self.assertEqual(scanner.result_code(
            [{"scope": "history", "findings": []}], strict=True), 0)

    def test_strict_command_overrides_repository_suppressions(self):
        command = scanner.scan_command(
            "gitleaks", "git", self.root, self.report,
            self.root / "defaults.toml", self.root / "empty-ignore", True,
        )
        self.assertIn("--all", self.log_options(command))
        self.assertIn("--ignore-gitleaks-allow", command)
        self.assertIn("--max-archive-depth=3", command)
        self.assertIn("--exit-code=10", command)
        self.assertIn("--redact=100", command)
        self.assertIn(str(self.root / "defaults.toml"), command)
        self.assertIn(str(self.root / "empty-ignore"), command)

    def test_diff_command_limits_history_to_requested_range(self):
        commit_range = "a" * 40 + ".." + "b" * 40
        command = scanner.scan_command(
            "gitleaks", "git", self.root, self.report,
            self.root / "config.toml", self.root / "ignore", False,
            log_range=commit_range,
        )
        self.assertIn(commit_range, self.log_options(command))
        self.assertNotIn("--all", self.log_options(command))
        self.assertNotIn("--verbose", command)

    def log_options(self, command):
        return shlex.split(next(arg.split("=", 1)[1] for arg in command
                                if arg.startswith("--log-opts=")))

    def git(self, repo, *args):
        return subprocess.check_output(
            ["git", "-C", str(repo), *args], stderr=subprocess.PIPE,
        ).decode("utf-8").strip()

    def commit_files(self, repo, message):
        self.git(repo, "add", ".")
        self.git(repo, "commit", "-qm", message)
        return self.git(repo, "rev-parse", "HEAD")

    def history_patch(self, repo, log_range=None):
        command = scanner.scan_command(
            "gitleaks", "git", repo, self.report,
            self.root / "config", self.root / "ignore", log_range is None,
            log_range=log_range,
        )
        options = self.log_options(command)
        return self.git(repo, "log", "-p", "-U0", *options)

    def test_history_and_diff_include_merge_only_additions(self):
        repo = self.init_repo()
        (repo / "safe").write_text("safe")
        base = self.commit_files(repo, "baseline")
        primary = self.git(repo, "branch", "--show-current")
        self.git(repo, "checkout", "-qb", "topic")
        (repo / "topic").write_text("safe")
        self.commit_files(repo, "topic")
        self.git(repo, "checkout", primary)
        (repo / "primary").write_text("safe")
        self.commit_files(repo, "primary")
        self.git(repo, "merge", "--no-commit", "--no-ff", "topic")
        (repo / "candidate").write_text("merge-only-synthetic-marker")
        merge = self.commit_files(repo, "merge")
        (repo / "candidate").unlink()
        self.commit_files(repo, "remove")
        for log_range in (None, base + ".." + merge):
            with self.subTest(log_range=log_range):
                self.assertIn("+merge-only-synthetic-marker", self.history_patch(repo, log_range))

    def test_attributes_cannot_hide_historical_text(self):
        repo = self.init_repo()
        (repo / ".gitattributes").write_text("candidate -diff\n")
        (repo / "candidate").write_text("attribute-hidden-synthetic-marker")
        self.commit_files(repo, "candidate")
        (repo / "candidate").unlink()
        self.commit_files(repo, "remove")
        self.assertIn("+attribute-hidden-synthetic-marker", self.history_patch(repo))

    def test_history_disables_external_diff_and_text_conversion(self):
        command = scanner.scan_command(
            "gitleaks", "git", self.root, self.report,
            self.root / "config", self.root / "ignore", True,
        )
        for option in ("--no-ext-diff", "--no-textconv"):
            self.assertIn(option, self.log_options(command))

    def test_history_keeps_binary_and_forced_text_scan_passes(self):
        seen = []

        def run(command, cwd=None):
            if command[0] == "git":
                return subprocess.CompletedProcess(command, 0, b"false\n", b"")
            text = "--text" in self.log_options(command)
            seen.append(text)
            finding = self.finding()
            finding["File"] = "text.txt" if text else "archive.zip"
            report = Path(command[command.index("--report-path") + 1])
            report.write_text(json.dumps([finding]), encoding="utf-8")
            return subprocess.CompletedProcess(command, 10, b"", b"")

        with mock.patch.object(scanner, "run_command", side_effect=run):
            scans = scanner.perform_scan(self.root, "gitleaks", False)
        self.assertEqual(set(seen), {False, True})
        self.assertEqual({f["File"] for f in scans[0]["findings"]},
                         {"archive.zip", "text.txt"})

    def test_error_in_either_history_pass_blocks(self):
        for failing_text in (False, True):
            with self.subTest(failing_text=failing_text):
                def run(command, cwd=None):
                    if command[0] == "git":
                        return subprocess.CompletedProcess(command, 0, b"false\n", b"")
                    text = "--text" in self.log_options(command)
                    report = Path(command[command.index("--report-path") + 1])
                    report.write_text("[]", encoding="utf-8")
                    return subprocess.CompletedProcess(
                        command, 2 if text == failing_text else 0, b"", b"",
                    )

                with mock.patch.object(scanner, "run_command", side_effect=run):
                    with self.assertRaises(scanner.ScanError):
                        scanner.perform_scan(self.root, "gitleaks", False)

    def test_history_passes_deduplicate_identical_locations(self):
        def run(command, cwd=None):
            if command[0] == "git":
                return subprocess.CompletedProcess(command, 0, b"false\n", b"")
            report = Path(command[command.index("--report-path") + 1])
            report.write_text(json.dumps([self.finding()]), encoding="utf-8")
            return subprocess.CompletedProcess(command, 10, b"", b"")

        with mock.patch.object(scanner, "run_command", side_effect=run):
            scans = scanner.perform_scan(self.root, "gitleaks", False)
        self.assertEqual(len(scans[0]["findings"]), 1)

    def test_workflows_fetch_commits_only_retained_by_pr_refs(self):
        for workflow, prefix in (
            (".github/workflows/ci.yml", "git fetch --force "),
        ):
            with self.subTest(workflow=workflow):
                self.assert_fetch_reaches_pr_only_commit(workflow, prefix)

    def assert_fetch_reaches_pr_only_commit(self, workflow, prefix):
        self.root = Path(tempfile.mkdtemp(dir=self.root))
        repo = self.init_repo()
        (repo / "safe").write_text("safe")
        base = self.commit_files(repo, "baseline")
        remote = self.root / "remote.git"
        self.git(repo, "init", "--bare", str(remote))
        self.git(repo, "remote", "add", "origin", str(remote))
        self.git(repo, "push", "origin", "HEAD:refs/heads/main")
        (repo / "candidate").write_text("pr-only-synthetic-marker")
        candidate = self.commit_files(repo, "candidate")
        self.git(repo, "push", "origin", "HEAD:refs/pull/23/head")
        clone = self.root / "clone"
        self.git(repo, "clone", "--branch", "main", str(remote), str(clone))
        self.assertEqual(self.git(clone, "rev-parse", "HEAD"), base)
        path = SCRIPT.parent.parent / workflow
        fetch = next(line.strip().removeprefix("run: ") for line in
                     path.read_text(encoding="utf-8").splitlines()
                     if line.strip().startswith(prefix))
        self.git(clone, *shlex.split(fetch)[1:])
        self.assertIn(candidate, self.git(clone, "rev-list", "--all").splitlines())

    def test_diff_findings_block_and_only_publish_location_metadata(self):
        finding = self.finding()
        self.write_report([finding])
        scans = [{"scope": "diff", "findings": scanner.load_findings(self.report, 10)}]
        with mock.patch.object(scanner, "perform_scan", return_value=scans):
            with contextlib.redirect_stdout(io.StringIO()):
                status = scanner.main([
                    "--repo", str(self.root), "--report", str(self.report),
                    "--fail-on-findings", "--log-range", "a" * 40 + ".." + "b" * 40,
                ])
        self.assertEqual(status, 1)
        output = self.report.read_text(encoding="utf-8")
        self.assertNotIn("sensitive-test-value", output)
        self.assertNotIn("private commit message", output)
        self.assertEqual(json.loads(output)["scans"][0]["findings"][0]["File"], "config.txt")

    def test_invalid_range_cannot_change_scan_options(self):
        with contextlib.redirect_stderr(io.StringIO()):
            for value in ("--all --since=yesterday", "HEAD", "a" * 40 + ".."):
                with self.subTest(value=value), self.assertRaises(SystemExit) as error:
                    scanner.main(["--log-range", value])
                self.assertEqual(error.exception.code, 2)
            with self.assertRaises(SystemExit) as error:
                scanner.main(["--strict", "--log-range", "a" * 40 + ".." + "b" * 40])
            self.assertEqual(error.exception.code, 2)

    def test_public_snapshot_includes_tracked_and_new_files_not_ignored_files(self):
        repo = self.root / "repo"
        repo.mkdir()
        subprocess.run(["git", "init", "-q", str(repo)], check=True)
        (repo / ".gitignore").write_text("private/\n", encoding="utf-8")
        (repo / "tracked.txt").write_text("old", encoding="utf-8")
        (repo / "deleted.txt").write_text("removed", encoding="utf-8")
        subprocess.run(["git", "-C", str(repo), "add", "."], check=True)
        (repo / "deleted.txt").unlink()
        (repo / "tracked.txt").write_text("edited", encoding="utf-8")
        (repo / "new.txt").write_text("new", encoding="utf-8")
        (repo / "private").mkdir()
        (repo / "private" / "secret").write_text("local", encoding="utf-8")
        target = self.root / "snapshot"
        scanner.copy_publishable_files(repo, target)
        self.assertEqual((target / "tracked.txt").read_text(), "edited")
        self.assertEqual((target / "new.txt").read_text(), "new")
        self.assertFalse((target / "private").exists())
        self.assertFalse((target / ".git").exists())
        self.assertFalse((target / "deleted.txt").exists())

    def test_snapshot_rejects_paths_outside_repository(self):
        completed = subprocess.CompletedProcess([], 0, b"../outside\0", b"")
        with mock.patch.object(scanner, "run_command", return_value=completed):
            with self.assertRaises(scanner.ScanError):
                scanner.copy_publishable_files(self.root, self.root / "snapshot")

    def init_repo(self):
        repo = self.root / "repo"
        repo.mkdir()
        subprocess.run(["git", "init", "-q", str(repo)], check=True)
        subprocess.run(["git", "-C", str(repo), "config", "user.name", "Test"], check=True)
        subprocess.run(
            ["git", "-C", str(repo), "config", "user.email", "test@example.invalid"],
            check=True,
        )
        return repo

    def test_staged_snapshot_keeps_content_hidden_by_worktree_edits(self):
        repo = self.init_repo()
        (repo / "changed.txt").write_text("safe", encoding="utf-8")
        (repo / "unchanged.txt").write_text("safe", encoding="utf-8")
        subprocess.run(["git", "-C", str(repo), "add", "."], check=True)
        subprocess.run(["git", "-C", str(repo), "commit", "-qm", "baseline"], check=True)
        (repo / "changed.txt").write_text("staged sensitive value", encoding="utf-8")
        (repo / "new.txt").write_text("new staged sensitive value", encoding="utf-8")
        subprocess.run(["git", "-C", str(repo), "add", "."], check=True)
        (repo / "changed.txt").write_text("safe again", encoding="utf-8")
        (repo / "new.txt").unlink()
        target = self.root / "staged"
        scanner.copy_staged_files(repo, target)
        self.assertEqual((target / "changed.txt").read_text(), "staged sensitive value")
        self.assertEqual((target / "new.txt").read_text(), "new staged sensitive value")
        self.assertFalse((target / "unchanged.txt").exists())

    def test_staged_snapshot_handles_initial_commit(self):
        repo = self.init_repo()
        (repo / "new.txt").write_text("staged value", encoding="utf-8")
        subprocess.run(["git", "-C", str(repo), "add", "."], check=True)
        (repo / "new.txt").unlink()
        target = self.root / "staged"
        scanner.copy_staged_files(repo, target)
        self.assertEqual((target / "new.txt").read_text(), "staged value")

    def test_staged_symlink_is_rejected_even_without_worktree_symlink(self):
        repo = self.init_repo()
        (repo / "link").write_text("target", encoding="utf-8")
        subprocess.run(["git", "-C", str(repo), "add", "."], check=True)
        oid = subprocess.check_output(
            ["git", "-C", str(repo), "rev-parse", ":link"], text=True,
        ).strip()
        subprocess.run(
            ["git", "-C", str(repo), "update-index", "--cacheinfo", f"120000,{oid},link"],
            check=True,
        )
        with self.assertRaises(scanner.ScanError):
            scanner.copy_staged_files(repo, self.root / "staged")

    def test_failed_rescan_removes_previous_success_report(self):
        self.report.write_text('{"schema_version": 1, "scans": []}', encoding="utf-8")
        with mock.patch.object(scanner, "perform_scan",
                               side_effect=scanner.ScanError("scanner failed")):
            with contextlib.redirect_stderr(io.StringIO()):
                status = scanner.main(["--repo", str(self.root), "--report", str(self.report)])
        self.assertEqual(status, 2)
        self.assertFalse(self.report.exists())

    def test_main_fails_closed_without_echoing_scanner_output(self):
        with mock.patch.object(scanner, "perform_scan",
                               side_effect=scanner.ScanError("scanner failed")):
            output = io.StringIO()
            with contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
                status = scanner.main(["--repo", str(self.root)])
        self.assertEqual(status, 2)
        self.assertNotIn("no findings", output.getvalue())

    def test_unexpected_error_is_scan_failure_without_details(self):
        with mock.patch.object(scanner, "perform_scan",
                               side_effect=RuntimeError("sensitive-test-value")):
            output = io.StringIO()
            with contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
                status = scanner.main(["--repo", str(self.root)])
        self.assertEqual(status, 2)
        self.assertNotIn("sensitive-test-value", output.getvalue())
        self.assertNotIn("Traceback", output.getvalue())

    def test_scanner_diagnostics_are_not_disclosed(self):
        shallow = subprocess.CompletedProcess([], 0, b"false\n", b"")
        failed = subprocess.CompletedProcess(
            [], 2, b"sensitive-test-value", b"sensitive-test-value",
        )
        with mock.patch.object(scanner, "run_command", side_effect=[shallow, failed]):
            with self.assertRaises(scanner.ScanError) as error:
                scanner.perform_scan(self.root, "gitleaks", False)
        self.assertNotIn("sensitive-test-value", str(error.exception))

    def test_error_diagnostic_cannot_pass_with_zero_exit_and_empty_report(self):
        def run(command, cwd=None):
            if command[0] == "git":
                return subprocess.CompletedProcess(command, 0, b"false\n", b"")
            report = Path(command[command.index("--report-path") + 1])
            report.write_text("[]", encoding="utf-8")
            return subprocess.CompletedProcess(
                command, 0, b"", b"ERR git failed: sensitive-test-value",
            )

        with mock.patch.object(scanner, "run_command", side_effect=run):
            with self.assertRaises(scanner.ScanError) as error:
                scanner.perform_scan(self.root, "gitleaks", False)
        self.assertNotIn("sensitive-test-value", str(error.exception))

    def test_unknown_commit_is_rejected_before_starting_scanner(self):
        repo = self.init_repo()
        with self.assertRaisesRegex(scanner.ScanError, "commit range"):
            scanner.perform_scan(repo, "not-installed", False, "0" * 40 + ".." + "1" * 40)

    def test_shallow_checkout_is_rejected(self):
        shallow = subprocess.CompletedProcess([], 0, b"true\n", b"")
        with mock.patch.object(scanner, "run_command", return_value=shallow):
            with self.assertRaises(scanner.ScanError):
                scanner.perform_scan(self.root, "gitleaks", True)

    def test_process_launch_failure_does_not_disclose_exception_details(self):
        with mock.patch.object(scanner.subprocess, "run",
                               side_effect=OSError("sensitive-test-value")):
            with self.assertRaises(scanner.ScanError) as error:
                scanner.run_command(["gitleaks"])
        self.assertNotIn("sensitive-test-value", str(error.exception))


if __name__ == "__main__":
    unittest.main()
