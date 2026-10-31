"""Real Git/Gitleaks regressions; requires the pinned Gitleaks executable."""

import hashlib
import json
import os
import secrets
import shutil
import string
import subprocess
import tempfile
import unittest
import zipfile
from pathlib import Path

from test_scan_secrets import scanner


class GitleaksHistoryTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.executable = os.environ.get("GITLEAKS_TEST_BINARY") or shutil.which("gitleaks")
        if not cls.executable:
            raise RuntimeError("Install Gitleaks or set GITLEAKS_TEST_BINARY; tests cannot be skipped.")

    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="sub2api-gitleaks-test-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.repo = self.root / "repo"
        self.repo.mkdir()
        self.git("init", "-q")
        self.git("config", "user.name", "Synthetic Test")
        self.git("config", "user.email", "synthetic@example.invalid")
        (self.repo / ".gitleaks.toml").write_text("[extend]\nuseDefault = true\n")
        self.base = self.commit("baseline")
        self.payload = "token=" + "ghp_" + "".join(
            secrets.choice(string.ascii_letters + string.digits) for _ in range(36)
        ) + "\n"

    def git(self, *args, repo=None):
        result = subprocess.run(
            ["git", "-C", str(repo or self.repo), *args], capture_output=True, check=True,
        )
        return result.stdout.decode().strip()

    def commit(self, message):
        self.git("add", ".")
        self.git("commit", "-qm", message)
        return self.git("rev-parse", "HEAD")

    def assert_historical_detection(self, repo=None, log_range=None):
        scans = scanner.perform_scan(
            repo or self.repo, self.executable, log_range is None, log_range,
        )
        self.assertGreater(len(scans[0]["findings"]), 0)
        for scan in scans[1:]:
            self.assertEqual(len(scan["findings"]), 0, scan["scope"])

    def test_deleted_compressed_archive(self):
        archive = self.repo / "candidate.zip"
        with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED) as output:
            output.writestr("candidate.txt", self.payload)
        commit = self.commit("archive")
        archive.unlink()
        self.commit("remove")
        self.assert_historical_detection()
        self.assert_historical_detection(log_range=self.base + ".." + commit)

    def test_deleted_text_hidden_by_attributes(self):
        (self.repo / ".gitattributes").write_text("candidate.txt -diff\n")
        candidate = self.repo / "candidate.txt"
        candidate.write_text(self.payload)
        self.commit("candidate")
        candidate.unlink()
        self.commit("remove")
        self.assert_historical_detection()

    def test_deleted_merge_only_addition(self):
        primary = self.git("branch", "--show-current")
        self.git("checkout", "-qb", "topic")
        (self.repo / "topic").write_text("safe")
        self.commit("topic")
        self.git("checkout", primary)
        (self.repo / "primary").write_text("safe")
        self.commit("primary")
        self.git("merge", "--no-ff", "--no-commit", "topic")
        candidate = self.repo / "candidate.txt"
        candidate.write_text(self.payload)
        merge = self.commit("merge")
        candidate.unlink()
        self.commit("remove")
        self.assert_historical_detection()
        self.assert_historical_detection(log_range=self.base + ".." + merge)

    def test_commit_retained_only_by_pr_ref(self):
        remote = self.root / "remote.git"
        self.git("init", "--bare", str(remote))
        self.git("remote", "add", "origin", str(remote))
        self.git("push", "origin", "HEAD:refs/heads/main")
        (self.repo / "candidate.txt").write_text(self.payload)
        self.commit("candidate")
        self.git("push", "origin", "HEAD:refs/pull/23/head")
        clone = self.root / "clone"
        self.git("clone", "--branch", "main", str(remote), str(clone))
        self.git("fetch", "origin", "+refs/pull/*:refs/remotes/public-review/pull/*", repo=clone)
        self.assert_historical_detection(repo=clone)

    def test_reviewed_checksum_allowlist_covers_merge_without_hiding_changes(self):
        project = Path(__file__).resolve().parent.parent
        (self.repo / ".gitleaks.toml").write_bytes((project / ".gitleaks.toml").read_bytes())
        manifest = json.loads((project / ".github/upgrade/manifest.json").read_text())
        migration = "backend/migrations/032_add_api_key_ip_restriction.sql"
        checksum = manifest["immutable_sha256"][migration]
        path = ".github/upgrade/manifest.json"
        candidate = self.repo / path

        def write_manifest(value, entry=migration, token=None):
            candidate.parent.mkdir(parents=True, exist_ok=True)
            data = {"immutable_sha256": {entry: value, "fixture": "safe"}}
            if token is not None:
                data["additional_api_key"] = token
            candidate.write_text(json.dumps(data, indent=2) + "\n")

        def findings(start, end, strict=False):
            return scanner.perform_scan(
                self.repo, self.executable, strict, start + ".." + end,
            )[0]["findings"]

        base = self.commit("reviewed policy")
        self.assertEqual(findings(self.base, base), [])
        primary = self.git("branch", "--show-current")
        self.git("checkout", "-qb", "reviewed-checksum")
        write_manifest(checksum)
        original = self.commit("reviewed checksum")
        self.assertEqual(findings(base, original), [])

        self.git("checkout", primary)
        (self.repo / "primary").write_text("safe")
        self.commit("primary advance")
        self.git("merge", "--no-ff", "-qm", "merge reviewed checksum", "reviewed-checksum")
        merge = self.git("rev-parse", "HEAD")
        self.assertEqual(findings(base, merge), [])

        strict = findings(base, merge, strict=True)
        self.assertEqual({
            finding["Commit"] for finding in strict
            if finding["File"] == path and finding["RuleID"] == "generic-api-key"
        }, {original, merge})

        # 相同已核验行出现在别的文件中仍须检出，证明路径与行条件是 AND。
        (self.repo / "other.json").write_bytes(candidate.read_bytes())
        other = self.commit("same checksum elsewhere")
        self.assertTrue(any(
            finding["File"] == "other.json" and finding["RuleID"] == "generic-api-key"
            for finding in findings(merge, other)
        ))

        write_manifest(hashlib.sha256(b"unreviewed fixture content").hexdigest())
        changed = self.commit("unreviewed checksum")
        self.assertTrue(any(
            finding["File"] == path and finding["RuleID"] == "generic-api-key"
            for finding in findings(other, changed)
        ))

        write_manifest(checksum, entry="backend/migrations/999_add_api_key_fixture.sql")
        renamed = self.commit("unreviewed checksum entry")
        self.assertTrue(any(
            finding["File"] == path and finding["RuleID"] == "generic-api-key"
            for finding in findings(changed, renamed)
        ))

        # 使用 setUp 的动态合成 PAT；其他规则不得因 generic-api-key 例外失效。
        write_manifest(checksum, token=self.payload.split("=", 1)[1].strip())
        token = self.commit("synthetic token alongside reviewed checksum")
        self.assertTrue(any(
            finding["File"] == path and finding["RuleID"] == "github-pat"
            for finding in findings(renamed, token)
        ))


if __name__ == "__main__":
    unittest.main()
