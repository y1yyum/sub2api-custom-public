#!/usr/bin/env python3
"""Verify the fixed CI receipt for the v0.4.0-rc.1 publication."""
import argparse
import base64
import json
import os
from pathlib import Path
import subprocess

REPOSITORY = 'y1yyum/sub2api-custom-public'
REPOSITORY_ID = 1433224805
TAG = 'v0.4.0-rc.1'
SOURCE = 'e41952662eb3c61af4be3c10a2be917febbc31db'
CI_HEAD = '2d698e56f290ecb854b5fabaeff3614b85abca01'
RUN = 41216246175
PR = 20
JOBS = {'Backend', 'Backend integration (network isolated)',
        'Backend unit (network isolated)', 'Custom upgrade safeguards',
        'Frontend', 'Frontend regression (Node 20)',
        'Frontend regression (Node 24)', 'Image smoke test'}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def validate(tag, source, run, jobs, pr, source_tree, ci_tree):
    require(tag == TAG and source == SOURCE, 'This receipt authorizes only the fixed rc.1 tag and commit')
    require(run['id'] == RUN and run['event'] == 'pull_request', 'Unexpected CI run')
    require(run['repository']['id'] == REPOSITORY_ID and
            run['head_repository']['id'] == REPOSITORY_ID, 'Foreign CI repository')
    require(run['path'] == '.github/workflows/ci.yml', 'Unexpected CI workflow')
    require(run['head_sha'] == CI_HEAD and run['status'] == 'completed' and
            run['conclusion'] == 'success', 'CI did not succeed on the exact reviewed head')
    require(len(jobs) == len(JOBS) and {job['name'] for job in jobs} == JOBS,
            'The complete eight-job CI receipt is required')
    require(all(job['run_id'] == RUN and job['status'] == 'completed' and
                job['conclusion'] == 'success' for job in jobs), 'A required job failed or skipped')
    require(pr['number'] == PR and pr['merged'] and pr['merge_commit_sha'] == SOURCE and
            pr['head']['sha'] == CI_HEAD, 'The source is not the reviewed protected merge')
    require(source_tree == ci_tree, 'CI and release source trees differ')
    return {'schema_version': '1', 'source_repository': REPOSITORY, 'tag': tag,
            'source_commit': source, 'source_tree': source_tree,
            'ci_run_id': RUN, 'ci_head_commit': CI_HEAD, 'ci_tree': ci_tree,
            'ci_url': run['html_url'], 'ci_conclusion': 'success',
            'ci_jobs': sorted(JOBS), 'merge_pr': PR,
            'reuse_reason': 'Identical complete Git tree after protected squash merge'}


def output(*args):
    return subprocess.check_output(args, text=True, encoding='utf-8').strip()


def api(path):
    return json.loads(output('gh', 'api', f'repos/{REPOSITORY}/{path}'))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--tag', required=True)
    parser.add_argument('--source-commit', required=True)
    parser.add_argument('--out', required=True)
    args = parser.parse_args()
    actual_tag = output('git', 'rev-parse', f'{args.tag}^{{commit}}')
    require(actual_tag == args.source_commit, 'Tag commit mismatch')
    run = api(f'actions/runs/{RUN}')
    jobs = api(f'actions/runs/{RUN}/jobs?per_page=100')['jobs']
    pr = api(f'pulls/{PR}')
    source_tree = output('git', 'rev-parse', f'{actual_tag}^{{tree}}')
    ci_tree = output('git', 'rev-parse', f'{CI_HEAD}^{{tree}}')
    evidence = validate(args.tag, actual_tag, run, jobs, pr, source_tree, ci_tree)
    evidence['publisher_workflow_ref'] = os.environ.get('GITHUB_WORKFLOW_REF', 'local-validation')
    evidence['publisher_workflow_commit'] = os.environ.get('GITHUB_SHA')
    encoded = json.dumps(evidence, ensure_ascii=False, separators=(',', ':'))
    Path(args.out).write_text(encoded + '\n', encoding='utf-8', newline='\n')
    if os.environ.get('GITHUB_OUTPUT'):
        with open(os.environ['GITHUB_OUTPUT'], 'a', encoding='utf-8') as stream:
            stream.write('evidence=' + base64.b64encode(encoded.encode()).decode() + '\n')
    print(json.dumps(evidence, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
