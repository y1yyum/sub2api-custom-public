#!/usr/bin/env python3
"""Record CI scope and verify full Local CI before granting publication."""
import argparse
import base64
from collections import defaultdict
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import zipfile

REPOSITORY = 'y1yyum/sub2api-custom-public'
REPOSITORY_ID = 1433224805
WORKFLOW = '.github/workflows/local-ci.yml'
JOBS = {'changes', 'unit', 'integration', 'golangci-lint', 'frontend',
        'security', 'shell', 'release-helpers', 'ci-gate'}
DEPENDENCIES = JOBS - {'ci-gate'}
FULL_SCOPE = {'unit': './...', 'unit_selected': '', 'integration': './...',
              'integration_selected': '', 'lint': './...', 'frontend': 'true',
              'shell': 'true', 'release': 'true'}
FULL_EVENTS = {'push', 'workflow_dispatch'}
ARTIFACT = 'local-ci-evidence-'


def require(condition, message):
    if not condition:
        raise ValueError(message)


def command(*args):
    result = subprocess.run(args, capture_output=True)
    if result.returncode:
        raise RuntimeError(f'{args[0]} {args[1]} failed ({result.returncode})')
    return result.stdout


def api(route, missing_ok=False):
    result = subprocess.run(['gh', 'api', f'repos/{REPOSITORY}/{route}'], capture_output=True)
    if missing_ok and result.returncode and b'HTTP 404' in result.stderr:
        return None
    if result.returncode:
        raise RuntimeError(f'GitHub API request failed: {route}')
    return json.loads(result.stdout)


def pages(route, key, request):
    rows, page = [], 1
    while True:
        data = request(f'{route}&per_page=100&page={page}')
        rows.extend(data[key])
        if len(rows) >= data['total_count']:
            return rows
        require(data[key], 'Incomplete CI response')
        page += 1


def record(commit, tree, event, scope, results, *, repository, repository_id, run_id, attempt):
    require(re.fullmatch(r'[0-9a-f]{40}', commit) and re.fullmatch(r'[0-9a-f]{40}', tree),
            'CI checkout identity is missing')
    require(set(scope) == set(FULL_SCOPE) and set(results) == DEPENDENCIES,
            'CI scope or result inventory differs')
    full = event in FULL_EVENTS and scope == FULL_SCOPE and all(value == 'success' for value in results.values())
    return {'schema_version': '1', 'repository': repository, 'repository_id': repository_id,
            'run_id': run_id, 'run_attempt': attempt, 'source_commit': commit, 'source_tree': tree,
            'event': event, 'mode': 'full' if full else 'scoped', 'scope': scope, 'jobs': results,
            'test_scope': 'tracked-public-snapshot'}


def check_identity(run, source):
    require(run['repository']['id'] == REPOSITORY_ID and
            (run.get('head_repository') or {}).get('id') == REPOSITORY_ID, 'Foreign CI repository')
    require(run['path'] == WORKFLOW and run['name'] == 'Local CI', 'Unexpected CI workflow')
    require(run['event'] in FULL_EVENTS, 'A scoped PR run cannot authorize publication')
    require(run['head_sha'] == source, 'CI and tag commits differ')


def latest_jobs(jobs, run, source):
    grouped = defaultdict(list)
    for job in jobs:
        require(job['run_id'] == run['id'] and job['head_sha'] == source, 'Foreign CI job')
        require(1 <= job['run_attempt'] <= run['run_attempt'], 'CI attempt changed during verification')
        grouped[job['name']].append(job)
    require(set(grouped) == JOBS, 'Complete Local CI job inventory is required')
    selected = []
    for name in sorted(JOBS):
        attempt = max(job['run_attempt'] for job in grouped[name])
        current = [job for job in grouped[name] if job['run_attempt'] == attempt]
        require(len(current) == 1, 'Duplicate job in the same CI attempt')
        job = current[0]
        if name == 'ci-gate':
            require(attempt == run['run_attempt'], 'The final gate did not run in this attempt')
        require(job['status'] == 'completed' and job['conclusion'] == 'success',
                f'CI job {name} did not succeed')
        selected.append({'name': name, 'job_id': job['id'], 'run_attempt': attempt, 'conclusion': 'success'})
    return selected


def read_receipt(run, *, request=api, download=command):
    name = ARTIFACT + str(run['run_attempt'])
    artifacts = request(f'actions/runs/{run["id"]}/artifacts?name={name}&per_page=100')['artifacts']
    matches = [item for item in artifacts if item['name'] == name and not item['expired']]
    require(len(matches) == 1, 'Full CI receipt is missing or expired; complete Local CI for this tag first')
    artifact = matches[0]
    require(artifact['workflow_run']['id'] == run['id'] and artifact['size_in_bytes'] <= 65536,
            'Invalid CI receipt archive')
    raw = download('gh', 'api', f'repos/{REPOSITORY}/actions/artifacts/{artifact["id"]}/zip')
    require(artifact.get('digest') == 'sha256:' + hashlib.sha256(raw).hexdigest(), 'CI receipt digest mismatch')
    with zipfile.ZipFile(io.BytesIO(raw)) as archive:
        require(archive.namelist() == ['ci-evidence.json'] and
                archive.getinfo('ci-evidence.json').file_size <= 65536, 'Unexpected CI receipt files')
        return json.loads(archive.read('ci-evidence.json'))


def verify(source, tree, run, jobs, receipt):
    check_identity(run, source)
    require(run['status'] == 'completed' and run['conclusion'] == 'success', 'Full Local CI has not succeeded')
    selected = latest_jobs(jobs, run, source)
    require(receipt.get('schema_version') == '1' and receipt.get('repository') == REPOSITORY and
            receipt.get('repository_id') == REPOSITORY_ID and receipt.get('run_id') == run['id'] and
            receipt.get('run_attempt') == run['run_attempt'], 'CI receipt identity mismatch')
    require(receipt.get('source_commit') == source and receipt.get('source_tree') == tree,
            'CI checkout differs from the release source')
    require(receipt.get('event') == run['event'] and receipt.get('mode') == 'full' and
            receipt.get('scope') == FULL_SCOPE and receipt.get('test_scope') == 'tracked-public-snapshot',
            'The CI receipt does not prove a full public-snapshot run')
    require(receipt.get('jobs') == {name: 'success' for name in DEPENDENCIES}, 'CI dependencies did not all succeed')
    return {'schema_version': '1', 'source_repository': REPOSITORY,
            'source_commit': source, 'source_tree': tree, 'ci_checkout_commit': source,
            'ci_run_id': run['id'], 'ci_run_attempt': run['run_attempt'], 'ci_mode': 'full',
            'ci_url': f'https://github.com/{REPOSITORY}/actions/runs/{run["id"]}',
            'ci_jobs': sorted(JOBS), 'ci_job_results': selected,
            'ci_tree': tree, 'test_scope': 'tracked-public-snapshot', 'ci_conclusion': 'success'}


def resolve(source, tree, trigger=None, *, request=api, receipt_reader=read_receipt):
    if trigger:
        check_identity(request(f'actions/runs/{trigger}'), source)
    rows = pages(f'actions/workflows/local-ci.yml/runs?head_sha={source}', 'workflow_runs', request)
    rows = [run for run in rows if run['head_sha'] == source and run['event'] in FULL_EVENTS]
    require(rows, 'No full Local CI run exists for this tag')
    run = max(rows, key=lambda item: (item['updated_at'], item['id']))
    check_identity(run, source)
    require(run['status'] == 'completed' and run['conclusion'] == 'success', 'Latest full CI is not successful')
    jobs = pages(f'actions/runs/{run["id"]}/jobs?filter=all', 'jobs', request)
    return verify(source, tree, run, jobs, receipt_reader(run))


def tag_source(tag, *, git=None):
    if git is None:
        git = lambda *args: command('git', *args).decode().strip()
    match = re.fullmatch(r'v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?', tag)
    require(match is not None, 'Expected a semantic version tag')
    prerelease = match.group(4)
    require(not prerelease or all(not part.isdigit() or part == '0' or not part.startswith('0')
                                 for part in prerelease.split('.')), 'Invalid numeric prerelease identifier')
    source = git('rev-parse', '--verify', f'refs/tags/{tag}^{{commit}}')
    require(re.fullmatch(r'[0-9a-f]{40}', source), 'Tag commit identity is missing')
    git('merge-base', '--is-ancestor', source, 'origin/main')
    version = git('show', source + ':backend/cmd/server/VERSION').strip()
    require(version == '.'.join(match.group(i) for i in (1, 2, 3)), 'Tag and VERSION differ')
    return {'tag': tag, 'version': tag[1:], 'commit': source,
            'date': git('log', '-1', '--format=%cI', source), 'prerelease': bool(prerelease)}, git('rev-parse', source + '^{tree}')


def prepare(tag, trigger=None, *, git=None, request=api, resolver=resolve):
    info, tree = tag_source(tag, git=git)
    info['published'] = request(f'releases/tags/{tag}', missing_ok=True) is not None
    if info['published']:
        return info, ''
    value = resolver(info['commit'], tree, trigger)
    value['tag'] = tag
    value['publisher_workflow_ref'] = os.environ['GITHUB_WORKFLOW_REF']
    value['publisher_workflow_commit'] = os.environ['GITHUB_SHA']
    encoded = base64.b64encode(json.dumps(value, separators=(',', ':')).encode()).decode()
    return info, encoded


def write(path, value, outputs=None):
    raw = json.dumps(value, ensure_ascii=False, separators=(',', ':'))
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(raw + '\n', encoding='utf-8', newline='\n')
    if outputs and os.environ.get('GITHUB_OUTPUT'):
        with open(os.environ['GITHUB_OUTPUT'], 'a', encoding='utf-8') as stream:
            for key, item in outputs.items():
                stream.write(f'{key}={item}\n')
    print(json.dumps(value, ensure_ascii=False, indent=2))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=['record', 'prepare', 'verify'])
    parser.add_argument('--tag')
    parser.add_argument('--source-commit')
    parser.add_argument('--trigger-run', type=int)
    parser.add_argument('--out', required=True)
    args = parser.parse_args()
    git = lambda *items: command('git', *items).decode().strip()
    if args.mode == 'prepare':
        require(args.tag, 'A release tag is required')
        info, encoded = prepare(args.tag, args.trigger_run)
        outputs = {key: str(value).lower() if isinstance(value, bool) else value for key, value in info.items()}
        outputs['evidence'] = encoded
        write(args.out, info, outputs)
        return
    if args.mode == 'record':
        commit = git('rev-parse', 'HEAD')
        require(commit == os.environ['GITHUB_SHA'], 'CI receipt checkout is not the event commit')
        scope = {key: os.environ['CI_SCOPE_' + key.upper()] for key in FULL_SCOPE}
        value = record(commit, git('rev-parse', 'HEAD^{tree}'), os.environ['GITHUB_EVENT_NAME'],
                       scope, json.loads(os.environ['CI_RESULTS']), repository=os.environ['GITHUB_REPOSITORY'],
                       repository_id=int(os.environ['GITHUB_REPOSITORY_ID']),
                       run_id=int(os.environ['GITHUB_RUN_ID']), attempt=int(os.environ['GITHUB_RUN_ATTEMPT']))
        write(args.out, value)
        return
    require(args.source_commit and re.fullmatch(r'[0-9a-f]{40}', args.source_commit), 'Missing release commit')
    value = resolve(args.source_commit, git('rev-parse', args.source_commit + '^{tree}'), args.trigger_run)
    value['publisher_workflow_ref'] = os.environ['GITHUB_WORKFLOW_REF']
    value['publisher_workflow_commit'] = os.environ['GITHUB_SHA']
    encoded = base64.b64encode(json.dumps(value, separators=(',', ':')).encode()).decode()
    write(args.out, value, {'evidence': encoded})


if __name__ == '__main__':
    main()
