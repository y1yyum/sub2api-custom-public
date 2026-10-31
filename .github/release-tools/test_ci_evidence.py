import base64
import copy
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import unittest
from unittest.mock import Mock, patch
import zipfile

spec = importlib.util.spec_from_file_location('ci_evidence', Path(__file__).with_name('ci_evidence.py'))
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


class EvidenceTests(unittest.TestCase):
    def setUp(self):
        self.source, self.tree = 'a' * 40, 'b' * 40
        self.results = {name: 'success' for name in m.DEPENDENCIES}
        self.receipt = m.record(self.source, self.tree, 'push', dict(m.FULL_SCOPE), self.results,
                                repository=m.REPOSITORY, repository_id=m.REPOSITORY_ID, run_id=99, attempt=1)
        self.run = {'id': 99, 'run_attempt': 1, 'name': 'Local CI', 'path': m.WORKFLOW,
                    'repository': {'id': m.REPOSITORY_ID}, 'head_repository': {'id': m.REPOSITORY_ID},
                    'event': 'push', 'head_sha': self.source, 'status': 'completed',
                    'conclusion': 'success', 'updated_at': '2026-10-31T00:00:00Z'}
        self.jobs = [{'id': i, 'name': name, 'run_id': 99, 'run_attempt': 1, 'head_sha': self.source,
                      'status': 'completed', 'conclusion': 'success'} for i, name in enumerate(sorted(m.JOBS), 1)]
        self.runs = [self.run]

    def verify(self):
        return m.verify(self.source, self.tree, self.run, self.jobs, self.receipt)

    def test_complete_non_pr_ci_proves_the_exact_tag_source(self):
        result = self.verify()
        self.assertEqual(result['source_commit'], self.source)
        self.assertEqual(result['ci_jobs'], sorted(m.JOBS))
        self.assertEqual(result['test_scope'], 'tracked-public-snapshot')

    def test_pr_is_scoped_even_if_every_job_and_scope_flag_is_full(self):
        receipt = m.record(self.source, self.tree, 'pull_request', dict(m.FULL_SCOPE), self.results,
                           repository=m.REPOSITORY, repository_id=m.REPOSITORY_ID, run_id=99, attempt=1)
        self.assertEqual(receipt['mode'], 'scoped')
        self.run['event'] = 'pull_request'
        self.receipt = dict(receipt, mode='full')
        with self.assertRaises(ValueError): self.verify()

    def test_selected_or_skipped_tests_never_become_full_receipts(self):
        for key, value in (('unit', './internal/service'), ('unit_selected', './internal/service'),
                           ('integration', ''), ('frontend', 'false')):
            scope = dict(m.FULL_SCOPE, **{key: value})
            receipt = m.record(self.source, self.tree, 'push', scope, self.results,
                               repository=m.REPOSITORY, repository_id=m.REPOSITORY_ID, run_id=99, attempt=1)
            self.assertEqual(receipt['mode'], 'scoped')
        self.results['unit'] = 'skipped'
        receipt = m.record(self.source, self.tree, 'push', dict(m.FULL_SCOPE), self.results,
                           repository=m.REPOSITORY, repository_id=m.REPOSITORY_ID, run_id=99, attempt=1)
        self.assertEqual(receipt['mode'], 'scoped')

    def test_wrong_repo_workflow_event_commit_or_run_state_is_rejected(self):
        for key, value in (('repository', {'id': 1}), ('head_repository', {'id': 1}),
                           ('path', '.github/workflows/fake.yml'), ('name', 'Other'),
                           ('event', 'pull_request_target'), ('head_sha', 'c' * 40),
                           ('status', 'in_progress'), ('conclusion', 'failure')):
            original = copy.deepcopy(self.run)
            self.run[key] = value
            with self.subTest(key=key), self.assertRaises(ValueError): self.verify()
            self.run = original

    def test_failed_missing_duplicate_skipped_or_foreign_job_is_rejected(self):
        original = self.jobs
        variants = [original[:-1], original + [dict(original[0], id=100)]]
        for key, value in (('conclusion', 'skipped'), ('conclusion', 'failure'),
                           ('run_id', 98), ('head_sha', 'c' * 40)):
            jobs = copy.deepcopy(original)
            jobs[0][key] = value
            variants.append(jobs)
        for jobs in variants:
            self.jobs = jobs
            with self.subTest(jobs=jobs), self.assertRaises(ValueError): self.verify()

    def test_partial_retry_uses_latest_job_results_and_final_gate_attempt(self):
        self.run['run_attempt'] = 2
        self.receipt['run_attempt'] = 2
        for name in ('unit', 'ci-gate'):
            job = next(job for job in self.jobs if job['name'] == name)
            job['conclusion'] = 'failure'
            self.jobs.append(dict(job, id=job['id'] + 100, run_attempt=2, conclusion='success'))
        result = self.verify()
        retried = {row['name']: row['run_attempt'] for row in result['ci_job_results']}
        self.assertEqual(retried['unit'], 2)
        self.assertEqual(retried['ci-gate'], 2)
        self.jobs[-1]['run_attempt'] = 1
        with self.assertRaises(ValueError): self.verify()

    def test_receipt_must_match_attempt_commit_tree_full_scope_and_dependencies(self):
        for key, value in (('repository_id', 1), ('run_id', 98), ('run_attempt', 2),
                           ('source_commit', 'c' * 40), ('source_tree', 'c' * 40),
                           ('mode', 'scoped'), ('scope', dict(m.FULL_SCOPE, unit='./one')),
                           ('jobs', dict(self.results, security='skipped'))):
            original = copy.deepcopy(self.receipt)
            self.receipt[key] = value
            with self.subTest(key=key), self.assertRaises(ValueError): self.verify()
            self.receipt = original

    def archive(self, receipt=None, extra=None):
        out = io.BytesIO()
        with zipfile.ZipFile(out, 'w') as archive:
            archive.writestr('ci-evidence.json', json.dumps(receipt or self.receipt))
            if extra: archive.writestr(extra, 'unexpected')
        return out.getvalue()

    def test_receipt_archive_must_be_current_immutable_and_data_only(self):
        raw = self.archive()
        artifact = {'id': 42, 'name': m.ARTIFACT + '1', 'expired': False, 'workflow_run': {'id': 99},
                    'size_in_bytes': len(raw), 'digest': 'sha256:' + hashlib.sha256(raw).hexdigest()}
        reader = lambda item, payload=raw: m.read_receipt(self.run,
                    request=lambda _: {'artifacts': [item]}, download=lambda *_: payload)
        self.assertEqual(reader(artifact), self.receipt)
        for change in ({'expired': True}, {'workflow_run': {'id': 98}}, {'size_in_bytes': 65537},
                       {'digest': 'sha256:' + '0' * 64}):
            with self.subTest(change=change), self.assertRaises(ValueError): reader(dict(artifact, **change))
        malicious = self.archive(extra='../code.py')
        changed = dict(artifact, size_in_bytes=len(malicious), digest='sha256:' + hashlib.sha256(malicious).hexdigest())
        with self.assertRaises(ValueError): reader(changed, malicious)

    def request(self, route):
        if route.startswith('actions/workflows/'):
            return {'workflow_runs': self.runs, 'total_count': len(self.runs)}
        if '/jobs?' in route:
            return {'jobs': self.jobs, 'total_count': len(self.jobs)}
        if route == 'actions/runs/99': return self.run
        self.fail(route)

    def test_latest_failed_run_cannot_reuse_older_success(self):
        self.runs.append(dict(self.run, id=100, updated_at='2026-10-31T00:01:00Z', conclusion='failure'))
        with self.assertRaises(ValueError):
            m.resolve(self.source, self.tree, request=self.request, receipt_reader=lambda _: self.receipt)

    def test_real_job_receipt_resolution_is_not_a_mutable_commit_status(self):
        result = m.resolve(self.source, self.tree, 99, request=self.request, receipt_reader=lambda _: self.receipt)
        self.assertEqual(result['ci_run_id'], 99)

    def fake_git(self, *args):
        if args[0] == 'rev-parse': return self.tree if args[-1].endswith('^{tree}') else self.source
        if args[0] == 'merge-base': return ''
        if args[0] == 'show':
            self.assertEqual(args[1], self.source + ':backend/cmd/server/VERSION')
            return '0.4.0'
        if args[0] == 'log': return '2026-10-31T00:00:00Z'
        self.fail(args)

    def test_invalid_tag_or_unmerged_source_never_reaches_publication(self):
        for tag in ('main', 'v0.4.0;true', 'v00.4.0', 'v0.4.0-rc.01'):
            with self.subTest(tag=tag), self.assertRaises(ValueError): m.tag_source(tag, git=Mock())
        def unmerged(*args):
            if args[0] == 'merge-base': raise RuntimeError('unmerged source')
            return self.fake_git(*args)
        with self.assertRaises(RuntimeError): m.tag_source('v0.4.0-rc.1', git=unmerged)

    def test_published_release_is_a_noop_without_ci_or_image_rebuild(self):
        resolver = Mock()
        info, encoded = m.prepare('v0.4.0-rc.1', git=self.fake_git,
                                  request=lambda *_args, **_kwargs: {'id': 1}, resolver=resolver)
        self.assertTrue(info['published'])
        self.assertEqual(encoded, '')
        resolver.assert_not_called()

    def test_publisher_identity_is_separate_from_application_source(self):
        with patch.dict('os.environ', {'GITHUB_WORKFLOW_REF': 'publisher@refs/heads/main', 'GITHUB_SHA': 'c' * 40}):
            info, encoded = m.prepare('v0.4.0-rc.1', git=self.fake_git,
                                      request=lambda *_args, **_kwargs: None,
                                      resolver=lambda *_: self.verify())
        evidence = json.loads(base64.b64decode(encoded))
        self.assertEqual(evidence['source_commit'], self.source)
        self.assertEqual(evidence['publisher_workflow_commit'], 'c' * 40)
        self.assertFalse(info['published'])


if __name__ == '__main__':
    unittest.main()
