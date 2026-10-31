"""只允许平台接入差异；以原始 Gitea 工作流为业务策略基准。"""
import copy
from pathlib import Path
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[2]
CHECKOUT = 'actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803'


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
        self.assertEqual(actual, expected)

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
