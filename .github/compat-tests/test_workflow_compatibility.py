"""只允许平台接入差异；以原始 Gitea 工作流为业务策略基准。"""
import copy
from pathlib import Path
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[2]
CHECKOUT = 'actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803'
UPLOAD = 'actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9'


def receipt_steps():
    scope = ('unit', 'unit_selected', 'integration', 'integration_selected', 'lint', 'frontend', 'shell', 'release')
    jobs = ('changes', 'unit', 'integration', 'golangci-lint', 'frontend', 'security', 'shell', 'release-helpers')
    import json
    env = {'CI_EVIDENCE_PATH': '${{ runner.temp }}/local-ci-evidence/ci-evidence.json',
           **{'CI_SCOPE_' + name.upper(): '${{ needs.changes.outputs.' + name + ' }}' for name in scope},
           'CI_RESULTS': json.dumps({name: '${{ needs.' + name + '.result }}' for name in jobs}, separators=(',', ':'))}
    return [
        {'name': 'Checkout CI receipt source', 'uses': CHECKOUT,
         'with': {'ref': '${{ github.sha }}', 'fetch-depth': 1, 'persist-credentials': False}},
        {'name': 'Record GitHub CI evidence', 'env': env,
         'run': 'python3 .github/release-tools/ci_evidence.py record --out "$CI_EVIDENCE_PATH"'},
        {'name': 'Upload GitHub CI evidence', 'uses': UPLOAD,
         'with': {'name': 'local-ci-evidence-${{ github.run_attempt }}',
                  'path': '${{ runner.temp }}/local-ci-evidence/ci-evidence.json',
                  'if-no-files-found': 'error', 'retention-days': 30}},
    ]


def remove_receipt_transport(job):
    if job['steps'][-3:] != receipt_steps():
        raise ValueError('Unexpected GitHub receipt transport')
    job['steps'] = job['steps'][:-3]


class WorkflowCompatibility(unittest.TestCase):
    def test_all_original_policy_and_commands_remain_identical(self):
        source = yaml.safe_load((ROOT / '.gitea/workflows/local-ci.yml').read_text(encoding='utf-8'))
        actual = yaml.safe_load((ROOT / '.github/workflows/local-ci.yml').read_text(encoding='utf-8'))
        expected = copy.deepcopy(source)
        for name in ('DOCKER_HOST', 'TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE'):
            expected['env'].pop(name)
        for name, job in expected['jobs'].items():
            job['runs-on'] = 'ubuntu-24.04'
            for step in job['steps']:
                if step.get('uses') == 'builtin:checkout':
                    step['uses'] = CHECKOUT
                    step.setdefault('with', {})['persist-credentials'] = False
            if name == 'changes':
                next(s for s in job['steps'] if s.get('id') == 'scope')['env']['CI_WORKFLOW'] = '.github/workflows/local-ci.yml'
        for job in actual['jobs'].values():
            job['steps'] = [step for step in job['steps'] if step.get('uses') != './.github/actions/setup-ci']
        remove_receipt_transport(actual['jobs']['ci-gate'])
        self.assertEqual(actual, expected)

    def test_receipt_transport_is_an_exact_platform_extension(self):
        workflow = yaml.safe_load((ROOT / '.github/workflows/local-ci.yml').read_text(encoding='utf-8'))
        steps = workflow['jobs']['ci-gate']['steps'][-3:]
        self.assertEqual(steps, receipt_steps())
        for index, key, value in ((0, 'uses', 'unreviewed/checkout@main'),
                                   (1, 'run', 'true'), (2, 'with', {'if-no-files-found': 'ignore'})):
            changed = copy.deepcopy(steps)
            changed[index][key] = value
            with self.assertRaises(ValueError):
                remove_receipt_transport({'steps': changed})

    def test_tool_versions_and_hashes_match_original_job_image(self):
        action = (ROOT / '.github/actions/setup-ci/action.yml').read_text(encoding='utf-8')
        for value in ('9.15.9', 'v8.30.1', 'v2.14.0', 'govulncheck@v1.9.0',
                      '551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb',
                      'ab90aeb7b066f92a33415b638a50fe5344bbb75a0d32ad30cc248d88f81032ab'):
            self.assertIn(value, action)
        metadata = yaml.safe_load(action)
        self.assertEqual(metadata['runs']['using'], 'composite')
        self.assertTrue(all(step.get('shell') == 'bash' for step in metadata['runs']['steps'] if 'run' in step))

    def test_only_the_supported_compatibility_inputs_are_used(self):
        action = yaml.safe_load((ROOT / '.github/actions/setup-ci/action.yml').read_text(encoding='utf-8'))
        workflow = yaml.safe_load((ROOT / '.github/workflows/local-ci.yml').read_text(encoding='utf-8'))
        for job in workflow['jobs'].values():
            for step in job['steps']:
                if step.get('uses') == './.github/actions/setup-ci':
                    self.assertLessEqual(set(step['with']), set(action['inputs']))
                    self.assertTrue(all(value == 'true' for value in step['with'].values()))


if __name__ == '__main__':
    unittest.main()
