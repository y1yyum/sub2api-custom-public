#!/usr/bin/env python3
"""Run Gitleaks without treating scanner failures as clean reports."""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path


class ScanError(Exception):
    pass


def run_command(command, cwd=None):
    try:
        return subprocess.run(
            command, cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            timeout=660, check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        # Tool output and exception strings can contain credentials or source text.
        raise ScanError("Command could not complete; check the installed tools.") from error


def load_findings(report, status):
    if status not in (0, 10):
        raise ScanError(f"Gitleaks failed with exit code {status}; scan is incomplete.")
    try:
        data = json.loads(report.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, ValueError) as error:
        raise ScanError("Gitleaks report is missing or invalid; scan is incomplete.") from error
    if not isinstance(data, list):
        raise ScanError("Gitleaks report must be a list.")
    findings = []
    for item in data:
        if (
            not isinstance(item, dict)
            or not isinstance(item.get("RuleID"), str) or not item["RuleID"]
            or not isinstance(item.get("File"), str) or not item["File"]
            or type(item.get("StartLine")) is not int or item["StartLine"] < 1
            or not isinstance(item.get("Commit", ""), str)
            or not isinstance(item.get("Fingerprint", ""), str)
        ):
            raise ScanError("Gitleaks finding has invalid location metadata.")
        findings.append({
            key: item[key]
            for key in ("RuleID", "File", "StartLine", "Commit", "Fingerprint")
            if key in item
        })
    if bool(findings) != (status == 10):
        raise ScanError("Gitleaks exit code disagrees with its report.")
    return findings


def scan_command(executable, mode, target, report, config, ignore, strict, log_range=None,
                 force_text=True):
    command = [
        executable, mode, str(target), "--config", str(config),
        "--redact=100", "--no-banner", "--no-color", "--exit-code=10",
        "--log-level=error",
        "--report-format=json", "--report-path", str(report), "--timeout=600",
        "--max-archive-depth=3", "--max-decode-depth=5",
    ]
    if mode == "git":
        # Include merge-only additions; repository diff attributes must not hide text.
        command.append("--log-opts=" + (log_range or "--all") +
                       " --full-history --diff-merges=separate --no-ext-diff --no-textconv" +
                       (" --text" if force_text else ""))
    if strict:
        command.extend([
            "--ignore-gitleaks-allow", "--gitleaks-ignore-path", str(ignore),
        ])
    return command


def copy_publishable_files(repo, target):
    result = run_command(
        ["git", "ls-files", "-z", "--cached", "--others", "--exclude-standard"],
        cwd=repo,
    )
    if result.returncode:
        raise ScanError("Could not enumerate publishable files.")
    target.mkdir(parents=True, exist_ok=True)
    for raw_path in set(result.stdout.split(b"\0")) - {b""}:
        path = Path(os.fsdecode(raw_path))
        source = repo / path
        if path.is_absolute() or ".." in path.parts or not source.resolve().is_relative_to(repo):
            raise ScanError("Snapshot path escapes the repository.")
        if source.is_symlink() or source.is_dir():
            raise ScanError("Symlinks and submodules require separate review; snapshot is incomplete.")
        if not source.exists():
            # Deleted worktree files remain covered by the history scan.
            continue
        destination = target / path
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, destination)


def copy_staged_files(repo, target):
    changed = run_command([
        "git", "diff", "--cached", "--name-only", "--diff-filter=d", "-z",
        "--no-ext-diff", "--no-textconv",
    ], cwd=repo)
    entries = run_command(["git", "ls-files", "--stage", "-z"], cwd=repo)
    if changed.returncode or entries.returncode:
        raise ScanError("Could not enumerate staged files.")
    paths = set(changed.stdout.split(b"\0")) - {b""}
    target.mkdir(parents=True, exist_ok=True)
    for entry in entries.stdout.split(b"\0"):
        if not entry:
            continue
        try:
            metadata, raw_path = entry.split(b"\t", 1)
            mode, oid, stage = metadata.split()
        except ValueError as error:
            raise ScanError("Invalid Git index metadata.") from error
        if stage != b"0" or mode not in (b"100644", b"100755"):
            raise ScanError("Unmerged files, symlinks and submodules require separate review.")
        if not re.fullmatch(rb"(?:[0-9a-f]{40}|[0-9a-f]{64})", oid):
            raise ScanError("Invalid Git index object ID.")
        if raw_path not in paths:
            continue
        path = Path(os.fsdecode(raw_path))
        destination = target / path
        if path.is_absolute() or ".." in path.parts or not destination.resolve().is_relative_to(target):
            raise ScanError("Staged snapshot path escapes the repository.")
        blob = run_command(["git", "cat-file", "blob", oid.decode("ascii")], cwd=repo)
        if blob.returncode:
            raise ScanError("Could not read a staged file.")
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_bytes(blob.stdout)


def perform_scan(repo, executable, strict, log_range=None):
    shallow = run_command(["git", "rev-parse", "--is-shallow-repository"], cwd=repo)
    if shallow.returncode or shallow.stdout.strip() != b"false":
        raise ScanError("A complete, non-shallow Git checkout is required.")
    if log_range:
        for oid in log_range.split(".."):
            resolved = run_command(["git", "cat-file", "-e", oid + "^{commit}"], cwd=repo)
            if resolved.returncode:
                raise ScanError("Could not resolve the requested commit range.")
    with tempfile.TemporaryDirectory(prefix="sub2api-secret-scan-") as temporary:
        root = Path(temporary)
        config = repo / ".gitleaks.toml"
        ignore = root / "empty-ignore"
        ignore.write_text("", encoding="utf-8")
        if strict:
            config = root / "defaults.toml"
            config.write_text("[extend]\nuseDefault = true\n", encoding="utf-8")
        scopes = [("diff" if log_range else "history", "git", repo)]
        if strict:
            staged = root / "staged"
            copy_staged_files(repo, staged)
            scopes.append(("staged", "dir", staged))
            snapshot = root / "snapshot"
            copy_publishable_files(repo, snapshot)
            scopes.append(("snapshot", "dir", snapshot))
        scans = []
        for scope, mode, target in scopes:
            unique = {}
            # Native binary diffs enable archive extraction; --text covers -diff attributes.
            for force_text in ((False, True) if mode == "git" else (True,)):
                report = root / f"{scope}-{int(force_text)}.json"
                command = scan_command(
                    executable, mode, target, report, config, ignore, strict, log_range,
                    force_text=force_text,
                )
                result = run_command(command, cwd=repo)
                # Gitleaks can log a Git error but still return 0 with an empty report.
                if result.stdout.strip() or result.stderr.strip():
                    raise ScanError("Gitleaks emitted error diagnostics; scan is incomplete.")
                for finding in load_findings(report, result.returncode):
                    unique[tuple(sorted(finding.items()))] = finding
            findings = list(unique.values())
            # Directory scans may return absolute paths into the temporary snapshot.
            if mode == "dir":
                for finding in findings:
                    path = Path(finding["File"])
                    if path.is_absolute() and path.is_relative_to(target):
                        finding["File"] = path.relative_to(target).as_posix()
                    finding.pop("Fingerprint", None)
            scans.append({"scope": scope, "findings": findings})
        return scans


def result_code(scans, strict):
    return int(strict and any(scan["findings"] for scan in scans))


def commit_range(value):
    oid = r"(?:[0-9a-f]{40}|[0-9a-f]{64})"
    if not re.fullmatch(oid + r"\.\." + oid, value):
        raise argparse.ArgumentTypeError("Expected two full commit IDs separated by '..'.")
    return value


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=Path, default=Path(__file__).resolve().parent.parent)
    parser.add_argument("--gitleaks", default="gitleaks")
    parser.add_argument("--report", type=Path, default=Path("gitleaks-history.json"))
    scope = parser.add_mutually_exclusive_group()
    scope.add_argument("--strict", action="store_true",
                       help="Scan without repository suppressions; block every finding.")
    scope.add_argument("--log-range", type=commit_range,
                       help="Scan only the specified commit range (base..head).")
    parser.add_argument("--fail-on-findings", action="store_true",
                        help="Block findings while retaining the repository rules.")
    args = parser.parse_args(argv)
    try:
        args.report.unlink(missing_ok=True)
        scans = perform_scan(args.repo.resolve(), args.gitleaks, args.strict, args.log_range)
        output = {
            "schema_version": 1, "strict": args.strict,
            "log_range": args.log_range, "scans": scans,
        }
        with tempfile.TemporaryDirectory(
            prefix=".secret-report-", dir=args.report.absolute().parent,
        ) as temporary:
            report = Path(temporary) / "report.json"
            report.write_text(json.dumps(output, indent=2) + "\n", encoding="utf-8")
            report.replace(args.report)
        count = sum(len(scan["findings"]) for scan in scans)
        message = f"Secret scan completed: {count} findings across {len(scans)} scan(s)."
        if count:
            message += " Findings require review; origin and validity are not established."
        message += "\nThis result does not certify that the repository is safe to publish.\n"
        print(message)
        if summary := os.environ.get("GITHUB_STEP_SUMMARY"):
            with open(summary, "a", encoding="utf-8") as stream:
                stream.write("### Secret scan\n\n" + message + "\n")
        return result_code(scans, args.strict or args.fail_on_findings)
    except (ScanError, OSError) as error:
        message = str(error) if isinstance(error, ScanError) else "Could not read or write scan files."
        print(f"Secret scan failed: {message}", file=sys.stderr)
        return 2
    except Exception:
        # Exit 1 means findings; keep unexpected failures distinct and quiet.
        print("Secret scan failed: unexpected internal error.", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
