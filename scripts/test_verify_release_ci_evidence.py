import copy
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('receipt', Path(__file__).with_name('verify-release-ci-evidence.py'))
receipt = importlib.util.module_from_spec(spec)
spec.loader.exec_module(receipt)


class ReceiptTests(unittest.TestCase):
    def setUp(self):
        self.run = {'id': receipt.RUN, 'event': 'pull_request',
                    'repository': {'id': receipt.REPOSITORY_ID},
                    'head_repository': {'id': receipt.REPOSITORY_ID},
                    'path': '.github/workflows/ci.yml', 'head_sha': receipt.CI_HEAD,
                    'status': 'completed', 'conclusion': 'success', 'html_url': 'https://github.com/example'}
        self.jobs = [{'name': name, 'run_id': receipt.RUN, 'status': 'completed',
                      'conclusion': 'success'} for name in receipt.JOBS]
        self.pr = {'number': receipt.PR, 'merged': True, 'merge_commit_sha': receipt.SOURCE,
                   'head': {'sha': receipt.CI_HEAD}}

    def verify(self, **changes):
        data = dict(tag=receipt.TAG, source=receipt.SOURCE, run=self.run,
                    jobs=self.jobs, pr=self.pr, source_tree='a' * 40, ci_tree='a' * 40)
        data.update(changes)
        return receipt.validate(**data)

    def test_accept_identical_tree_and_complete_trusted_run(self):
        self.assertEqual(self.verify()['ci_run_id'], receipt.RUN)

    def test_reject_moved_tag_or_different_release(self):
        for change in ({'source': 'b' * 40}, {'tag': 'v0.4.0-rc.2'}):
            with self.subTest(change=change), self.assertRaises(ValueError):
                self.verify(**change)

    def test_reject_foreign_repository_or_workflow(self):
        for key, value in (('repository', {'id': 1}), ('head_repository', {'id': 1}),
                           ('path', '.github/workflows/other.yml')):
            run = copy.deepcopy(self.run)
            run[key] = value
            with self.subTest(key=key), self.assertRaises(ValueError):
                self.verify(run=run)

    def test_reject_failed_skipped_or_missing_job(self):
        for conclusion in ('failure', 'skipped', 'cancelled'):
            jobs = copy.deepcopy(self.jobs)
            jobs[0]['conclusion'] = conclusion
            with self.subTest(conclusion=conclusion), self.assertRaises(ValueError):
                self.verify(jobs=jobs)
        with self.assertRaises(ValueError):
            self.verify(jobs=self.jobs[:-1])

    def test_reject_stale_head_or_unmerged_pr(self):
        self.run['head_sha'] = 'b' * 40
        with self.assertRaises(ValueError):
            self.verify()
        self.run['head_sha'] = receipt.CI_HEAD
        self.pr['merged'] = False
        with self.assertRaises(ValueError):
            self.verify()

    def test_reject_source_difference(self):
        with self.assertRaises(ValueError):
            self.verify(ci_tree='b' * 40)


if __name__ == '__main__':
    unittest.main(verbosity=2)
