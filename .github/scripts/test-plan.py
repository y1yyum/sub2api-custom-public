"""隔离编译、运行及宿主审计共用的测试范围契约。"""
from collections import Counter
import re

MODULE = 'github.com/Wei-Shaw/sub2api/'
SERVICE = MODULE + 'internal/service'
FIXED = {
    'unit': ['TestGroupUsageDateUsesConfiguredTimezoneBoundary', 'TestGroupUsageParseDateUsesConfiguredTimezone',
             'TestGroupUsageYesterdayStartHandlesDST', 'TestSanitizeOpenAIResponsesToolParameterTypes_RewriteCountIndependentOfHits'],
    'integration': ['TestSanitizeOpenAIResponsesToolParameterTypes_RewriteCountIndependentOfHits'],
}


def validate(plan, suite, commit):
    if not isinstance(plan, dict) or plan.get('mode') not in {'full', 'fast'}:
        raise ValueError('invalid test plan mode')
    if plan.get('suite') != suite or plan.get('source_commit') != commit:
        raise ValueError('test plan suite or source mismatch')
    if type(plan.get('shards')) is not int or not 1 <= plan['shards'] <= 4:
        raise ValueError('invalid shard count')
    packages = plan.get('packages')
    if plan['mode'] == 'full':
        if packages is not None:
            raise ValueError('full plan must discover every package')
    elif not isinstance(packages, dict) or not packages:
        raise ValueError('fast plan needs an explicit nonempty package set')
    for package, names in (packages or {}).items():
        if (not isinstance(package, str) or not package.startswith(MODULE) or
                not re.fullmatch(r'[A-Za-z0-9_./-]+', package) or '..' in package.split('/')):
            raise ValueError('invalid package selector')
        if names is not None and (not isinstance(names, list) or not names or len(names) != len(set(names)) or
                                  any(not isinstance(n, str) or not re.fullmatch(r'(Test|Example|Fuzz)\w*', n) for n in names)):
            raise ValueError('invalid named test selection')
    slow = plan.get('slow_tests')
    if not isinstance(slow, dict):
        raise ValueError('slow test inventory required')
    for package, names in slow.items():
        if not package.startswith(MODULE) or not isinstance(names, list) or len(names) != len(set(names)):
            raise ValueError('invalid slow test package')
        if any(not re.fullmatch(r'Test\w+', name) for name in names):
            raise ValueError('invalid slow test name')
    return plan


def package_args(plan):
    return ['./...'] if plan is None or plan['mode'] == 'full' else sorted(plan['packages'])


def memberships(plan, package, registry):
    if len(registry) != len(set(registry)):
        raise ValueError('duplicate test registry')
    requested = None if plan['mode'] == 'full' else plan['packages'][package]
    selected = sorted(registry if requested is None else requested)
    if not set(selected) <= set(registry):
        raise ValueError('selected test missing from the compiled binary')
    fixed = [n for n in FIXED[plan['suite']] if package == SERVICE and n in selected]
    if package == SERVICE and plan['mode'] == 'full' and fixed != FIXED[plan['suite']]:
        raise ValueError('fixed service fixture missing or renamed')
    slow = sorted((set(plan['slow_tests'].get(package, [])) & set(selected)) - set(fixed))
    if plan['mode'] != 'full':
        slow = []
    rest = sorted(set(selected) - set(fixed) - set(slow))
    partitions = []
    if rest:
        partitions.append({'name': 'main', 'tests': rest})
    for i in range(plan['shards']):
        names = slow[i::plan['shards']]
        if names:
            partitions.append({'name': 'slow-' + str(i + 1), 'tests': names})
    partitions.extend({'name': name, 'tests': [name]} for name in fixed)
    if Counter(n for p in partitions for n in p['tests']) != Counter(selected):
        raise ValueError('partitions do not cover the selected tests exactly once')
    return selected, partitions


def audit(receipt, terminals, plan, compiled):
    validate(plan, receipt['suite'], receipt['source_commit'])
    audits = receipt.get('planned_package_audits', [])
    records = compiled.get('packages', [])
    if plan['mode'] == 'fast' and {r['package'] for r in records} != set(plan['packages']):
        raise ValueError('compiled packages differ from the selected plan')
    wanted = {r['package']: r for r in records if r['binary'] is not None}
    if len(audits) != len(wanted) or {a['package'] for a in audits} != set(wanted):
        raise ValueError('missing or duplicate package discovery evidence')
    results = receipt.get('package_results', [])
    for item in audits:
        record = wanted[item['package']]
        if item.get('binary_sha256') != record['binary_sha256'] or item.get('discovery_exit_code') != 0:
            raise ValueError('package discovery provenance mismatch')
        selected, parts = memberships(plan, item['package'], item['registry'])
        if item['selected'] != selected or len(parts) != len(item['partitions']):
            raise ValueError('selected coverage differs from the source plan')
        for expected, part in zip(parts, item['partitions']):
            if part['name'] != expected['name'] or part['tests'] != expected['tests'] or part.get('go_regex_verified') is not True:
                raise ValueError('partition differs from the immutable schedule')
            if sorted(n for n in item['registry'] if re.fullmatch(part['regex'], n)) != expected['tests']:
                raise ValueError('partition regular expression differs from its coverage')
        actual = Counter()
        for name in item['registry']:
            actual[name] = terminals[(item['package'], name, 'run')]
        if +actual != Counter(selected):
            raise ValueError('actual test runs differ from the selected coverage')
        for name in selected:
            counts = [terminals[(item['package'], name, action)] for action in ('pass', 'fail', 'skip')]
            if sum(counts) != 1 or counts[1] != 0:
                raise ValueError('missing or duplicate test terminal event')
        ended = [r for r in results if r['package'] == item['package']]
        if Counter(r.get('partition') for r in ended) != Counter(p['name'] for p in parts):
            raise ValueError('missing or duplicate partition exit')
        if any(r.get('binary_sha256') != record['binary_sha256'] or r.get('exit_code') != 0 for r in ended):
            raise ValueError('failed partition or mismatched binary')
    return {'status': 'passed', 'mode': plan['mode'], 'package_count': len(wanted),
            'selected_top_level_tests': sum(len(a['selected']) for a in audits),
            'unselected_top_level_tests': sum(len(a['registry']) - len(a['selected']) for a in audits)}
