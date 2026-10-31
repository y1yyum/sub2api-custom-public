from pathlib import Path
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[2]


class PublishWorkflowTests(unittest.TestCase):
    def setUp(self):
        self.workflow = yaml.safe_load((ROOT / '.github/workflows/publish.yml').read_text(encoding='utf-8'))

    def test_completion_handoff_never_launches_another_ci_workflow(self):
        triggers = self.workflow[True]
        self.assertEqual(set(triggers), {'workflow_run', 'workflow_dispatch'})
        self.assertEqual(triggers['workflow_run'], {'workflows': ['Local CI'], 'types': ['completed']})
        self.assertTrue(all('uses' not in job for job in self.workflow['jobs'].values()))

    def test_readonly_controller_precedes_main_ancestor_and_ci_validation(self):
        prepare = self.workflow['jobs']['prepare']
        checkout = prepare['steps'][0]
        self.assertEqual(checkout['with']['ref'], '${{ github.sha }}')
        self.assertFalse(checkout['with']['persist-credentials'])
        run = prepare['steps'][1]['run']
        self.assertIn('ARGS=(prepare --tag "$SOURCE_TAG"', run)
        self.assertIn('ci_evidence.py "${ARGS[@]}"', run)
        self.assertEqual(self.workflow['permissions'], {'contents': 'read', 'actions': 'read'})

    def test_published_source_and_security_gate_control_image_build(self):
        jobs = self.workflow['jobs']
        self.assertEqual(jobs['image']['needs'], ['prepare', 'security_refresh'])
        self.assertEqual(jobs['security_refresh']['needs'], 'prepare')
        self.assertEqual(jobs['security_refresh']['if'], "needs.prepare.outputs.published != 'true'")
        self.assertEqual(jobs['image']['steps'][0]['with']['ref'], '${{ needs.prepare.outputs.commit }}')
        names = [step.get('name') for step in jobs['image']['steps']]
        self.assertLess(names.index('Sign image'), names.index('Tag image'))
        self.assertLess(names.index('Generate SBOM'), names.index('Tag image'))

    def test_only_the_publisher_jobs_can_write_delivery_artifacts(self):
        jobs = self.workflow['jobs']
        self.assertEqual(jobs['security_refresh']['permissions'], {'contents': 'read'})
        self.assertEqual(jobs['image']['permissions'], {'contents': 'read', 'packages': 'write',
                         'id-token': 'write', 'attestations': 'write'})
        self.assertEqual(jobs['release']['permissions'], {'contents': 'write'})


if __name__ == '__main__':
    unittest.main()
