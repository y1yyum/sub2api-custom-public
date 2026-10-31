#!/usr/bin/env python3
"""Verify that a PR's required ci-gate status really comes from Gitea Actions.

Gitea branch protection matches required statuses by context name only, so
any role with write access can satisfy `Local CI / ci-gate (pull_request)`
through `POST /statuses/{sha}`. agent-coordinator runs this check before
every merge (Issue #70); `--merge` merges only after it passes.

Tokens stay inside the local helper (`gapi.py`); this script never reads
them. Run and job records come from targeted read-only SQL against the
Gitea database because the Actions API is not readable by the agent roles.

Exit codes: 0 pass, 1 blocked, 3 usage or environment error.
"""
import argparse
import json
import os
import pathlib
import re
import subprocess
import sys

REPO = "sub2api-ai/sub2api-custom"
BASE_BRANCH = "main"
CONTEXT = "Local CI / ci-gate (pull_request)"
GATE_JOB_ID = "ci-gate"
ACTIONS_CREATOR_ID = -2
ACTIONS_SUCCESS = 1
# Gitea records a synchronize push as pull_request_sync but still reports
# the status under the pull_request context (Issue #74).
PR_EVENTS = ("pull_request", "pull_request_sync")
DEFAULT_GAPI = pathlib.Path.home() / ".local" / "share" / "sub2api-gitea" / "gapi.py"
DEFAULT_DB_CONTAINER = "local-gitea-postgres"
SHA_RE = re.compile(r"[0-9a-f]{40}")


class EnvError(Exception):
    pass


def evaluate(pr, head, repo_id, statuses, jobs, repo=REPO):
    """Return the reasons to block; an empty list means the gate is genuine."""
    problems = [
        f"status {s['id']} ({s['context']}, {s['state']}) was written by user "
        f"{s['creator_id']}, not Actions"
        for s in statuses
        if s["creator_id"] != ACTIONS_CREATOR_ID
    ]
    gate = [
        s for s in statuses
        if s["context"] == CONTEXT and s["creator_id"] == ACTIONS_CREATOR_ID
    ]
    if not gate:
        return problems + [f"no Actions status for {CONTEXT!r} on {head}"]

    latest = max(gate, key=lambda s: s["id"])
    if latest["state"] != "success":
        problems.append(f"latest {CONTEXT!r} is {latest['state']!r} (status {latest['id']})")

    match = re.fullmatch(
        rf"/{re.escape(repo)}/actions/runs/(\d+)/jobs/(\d+)", latest["target_url"] or ""
    )
    if not match:
        return problems + [f"target_url {latest['target_url']!r} is not a job of {repo}"]
    run_id, job_id = int(match[1]), int(match[2])

    job = jobs.get(job_id)
    if job is None:
        return problems + [f"job {job_id} from target_url does not exist"]

    expected = {
        "job.run_id": run_id,
        "job.repo_id": repo_id,
        "job.job_id": GATE_JOB_ID,
        "job.commit_sha": head,
        "job.status": ACTIONS_SUCCESS,
        "run.repo_id": repo_id,
        "run.ref": f"refs/pull/{pr}/head",
        "run.commit_sha": head,
        "run.status": ACTIONS_SUCCESS,
    }
    problems += [
        f"{key} is {job.get(key)!r}, expected {want!r}"
        for key, want in expected.items()
        if job.get(key) != want
    ]
    if job.get("run.event") not in PR_EVENTS:
        problems.append(f"run.event is {job.get('run.event')!r}, expected one of {PR_EVENTS}")
    return problems


def gapi(helper, method, path, body=None):
    cmd = [sys.executable, str(helper), "coordinator", method, path]
    if body is not None:
        cmd.append(json.dumps(body))
    env = dict(os.environ, PYTHONIOENCODING="utf-8")
    proc = subprocess.run(cmd, capture_output=True, encoding="utf-8", env=env)
    code, _, payload = proc.stdout.partition("\n")
    if not code.strip().isdigit():
        raise EnvError(f"gapi {method} {path} failed: {proc.stderr.strip() or proc.stdout.strip()}")
    return int(code), payload


def sql_json(container, query):
    proc = subprocess.run(
        ["docker", "exec", "-i", container, "sh", "-c",
         'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At'],
        input=query, capture_output=True, encoding="utf-8",
    )
    if proc.returncode != 0:
        raise EnvError(f"SQL failed: {proc.stderr.strip()}")
    return json.loads(proc.stdout.strip() or "null")


def fetch_pr(helper, pr):
    code, payload = gapi(helper, "GET", f"/repos/{REPO}/pulls/{pr}")
    if code != 200:
        raise EnvError(f"GET pull {pr} returned {code}: {payload.strip()[:200]}")
    return json.loads(payload)


def collect(container, pr, head):
    owner, name = REPO.split("/")
    repo_id = sql_json(container, (
        "select to_json(id) from repository "
        f"where owner_name = '{owner}' and lower_name = '{name.lower()}';"
    ))
    if not isinstance(repo_id, int):
        raise EnvError(f"repository {REPO} not found in the Gitea database")
    statuses = sql_json(container, (
        "select coalesce(json_agg(t order by t.id), '[]') from ("
        "select id, state, creator_id, context, coalesce(target_url, '') as target_url "
        f"from commit_status where repo_id = {repo_id} and sha = '{head}') t;"
    ))
    job_ids = sorted({
        int(m[1])
        for s in statuses
        if s["context"] == CONTEXT
        for m in [re.search(r"/jobs/(\d+)$", s["target_url"])]
        if m
    })
    jobs = {}
    if job_ids:
        rows = sql_json(container, (
            "select coalesce(json_agg(t), '[]') from (select "
            'j.id as "job.id", j.run_id as "job.run_id", j.repo_id as "job.repo_id", '
            'j.job_id as "job.job_id", j.commit_sha as "job.commit_sha", '
            'j.status as "job.status", r.index as "run.index", '
            'r.repo_id as "run.repo_id", r.event as "run.event", r.ref as "run.ref", '
            'r.commit_sha as "run.commit_sha", r.status as "run.status" '
            "from action_run_job j left join action_run r on r.id = j.run_id "
            f"where j.id in ({', '.join(map(str, job_ids))})) t;"
        ))
        jobs = {row["job.id"]: row for row in rows}
    return repo_id, statuses, jobs


def check(args):
    helper = pathlib.Path(args.gapi)
    if not helper.is_file():
        raise EnvError(f"helper not found: {helper}")

    if args.sha:
        head = args.sha.lower()
        if not SHA_RE.fullmatch(head):
            raise EnvError("--sha must be a full 40-character commit SHA")
    else:
        pull = fetch_pr(helper, args.pr)
        if pull.get("state") != "open" or pull.get("merged"):
            raise EnvError(f"PR #{args.pr} is not open")
        if pull["base"]["ref"] != BASE_BRANCH:
            raise EnvError(f"PR #{args.pr} targets {pull['base']['ref']!r}, not {BASE_BRANCH!r}")
        head = pull["head"]["sha"]

    repo_id, statuses, jobs = collect(args.db_container, args.pr, head)
    problems = evaluate(args.pr, head, repo_id, statuses, jobs)

    print(f"PR #{args.pr} head {head}")
    for job in jobs.values():
        print(f"  ci-gate job {job['job.id']} -> run #{job['run.index']} "
              f"({job['run.event']}, {job['run.ref']})")
    if problems:
        print("BLOCKED:")
        for problem in problems:
            print(f"  - {problem}")
        return 1, head
    print(f"OK: {CONTEXT!r} comes from Actions on the current head")
    return 0, head


def merge(args, head):
    pull = fetch_pr(args.gapi, args.pr)
    if pull["head"]["sha"] != head:
        raise EnvError(f"head moved to {pull['head']['sha']} during the check; rerun it")
    code, payload = gapi(args.gapi, "POST", f"/repos/{REPO}/pulls/{args.pr}/merge",
                         {"Do": "merge", "head_commit_id": head})
    print(f"merge returned {code} {payload.strip()[:300]}")
    return 0 if code == 200 else 1


def selftest():
    head = "a" * 40
    other = "b" * 40
    good_url = f"/{REPO}/actions/runs/153/jobs/1279"
    job = {
        "job.id": 1279, "job.run_id": 153, "job.repo_id": 2, "job.job_id": "ci-gate",
        "job.commit_sha": head, "job.status": 1, "run.index": 147, "run.repo_id": 2,
        "run.event": "pull_request", "run.ref": "refs/pull/69/head",
        "run.commit_sha": head, "run.status": 1,
    }

    def status(sid, state="success", creator=ACTIONS_CREATOR_ID, url=good_url, context=CONTEXT):
        return {"id": sid, "state": state, "creator_id": creator, "context": context,
                "target_url": url}

    base = [status(1, "pending"), status(2, context="Local CI / image (pull_request)"),
            status(3)]

    def with_job(**changes):
        return {1279: {**job, **changes}}

    cases = [
        ("genuine success", base, {1279: job}, None),
        ("rerun: older failure then success", [status(1, "failure"), status(2)], {1279: job}, None),
        ("spoofed success, no run", [status(1, creator=3, url="")], {}, "not Actions"),
        ("spoofed status next to real run", base + [status(4, creator=3, url="")],
         {1279: job}, "written by user 3"),
        ("spoofed other context", base + [status(4, creator=3, context="x")],
         {1279: job}, "written by user 3"),
        ("no status", [], {}, "no Actions status"),
        ("latest pending", base + [status(4, "pending")], {1279: job}, "'pending'"),
        ("latest failure", base + [status(4, "failure")], {1279: job}, "'failure'"),
        ("other repo url", [status(1, url="/evil/repo/actions/runs/153/jobs/1279")],
         {1279: job}, "not a job of"),
        ("malformed url", [status(1, url=good_url + "/x")], {1279: job}, "not a job of"),
        ("unknown job", base, {}, "does not exist"),
        ("job of another run", base, with_job(**{"job.run_id": 99}), "job.run_id"),
        ("job not ci-gate", base, with_job(**{"job.job_id": "contract"}), "job.job_id"),
        ("job on another commit", base, with_job(**{"job.commit_sha": other}), "job.commit_sha"),
        ("job not success", base, with_job(**{"job.status": 2}), "job.status"),
        ("run in another repo", base, with_job(**{"run.repo_id": 3}), "run.repo_id"),
        ("synchronize run", base, with_job(**{"run.event": "pull_request_sync"}), None),
        ("push run", base, with_job(**{"run.event": "push"}), "run.event"),
        ("pull_request_target run", base,
         with_job(**{"run.event": "pull_request_target"}), "run.event"),
        ("run without event", base, with_job(**{"run.event": None}), "run.event"),
        ("run of another PR", base, with_job(**{"run.ref": "refs/pull/70/head"}), "run.ref"),
        ("run on another commit", base, with_job(**{"run.commit_sha": other}), "run.commit_sha"),
        ("run still running", base, with_job(**{"run.status": 6}), "run.status"),
        ("run deleted", base, with_job(**{"run.status": None}), "run.status"),
    ]
    failures = 0
    for name, statuses, jobs, expect in cases:
        problems = evaluate(69, head, 2, statuses, jobs)
        ok = not problems if expect is None else any(expect in p for p in problems)
        if not ok:
            failures += 1
            print(f"FAIL {name}: {problems}")
    if failures:
        print(f"selftest: {failures} of {len(cases)} cases failed")
        return 1
    print(f"selftest: {len(cases)} cases passed")
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--pr", type=int, help="pull request number")
    parser.add_argument("--sha", help="audit this commit as PR head (no PR state check, no merge)")
    parser.add_argument("--merge", action="store_true", help="merge as agent-coordinator after a pass")
    parser.add_argument("--gapi", default=os.environ.get("SUB2API_GAPI", str(DEFAULT_GAPI)))
    parser.add_argument("--db-container", default=DEFAULT_DB_CONTAINER)
    parser.add_argument("--selftest", action="store_true", help="run offline cases and exit")
    args = parser.parse_args()

    if args.selftest:
        return selftest()
    if args.pr is None or args.pr <= 0:
        parser.print_usage(sys.stderr)
        print("--pr is required", file=sys.stderr)
        return 3
    if args.merge and args.sha:
        print("--merge cannot be combined with --sha", file=sys.stderr)
        return 3
    try:
        rc, head = check(args)
        if rc == 0 and args.merge:
            rc = merge(args, head)
        return rc
    except EnvError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 3


if __name__ == "__main__":
    sys.exit(main())
