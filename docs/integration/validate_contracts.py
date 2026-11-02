#!/usr/bin/env python3
"""Validate the INT-00 inventory, not runtime acceptance. No external calls."""
import hashlib
import json
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[2]


def require(condition, message):
    if not condition:
        raise ValueError(message)


def bound(path, digest):
    require(isinstance(path, str) and not Path(path).is_absolute(), 'invalid bound path')
    candidate = (ROOT / path).resolve()
    require(candidate.is_relative_to(ROOT), 'bound path escapes repository')
    require(candidate.is_file(), f'missing bound input: {path}')
    require(hashlib.sha256(candidate.read_bytes()).hexdigest() == digest,
            f'changed input requires inventory review: {path}')


def validate():
    doc = json.loads((ROOT / 'docs/integration/contracts.json').read_text())
    source = doc['historical_source']
    bound(source['path'], source['sha256'])
    historical = json.loads((ROOT / source['path']).read_text())
    expected = []
    for group in historical['test_reclassifications']:
        package = 'github.com/Wei-Shaw/sub2api/' + str(Path(group['path']).parent).removeprefix('backend/')
        expected.extend((package, name) for name in group['test_functions'])
    expected.extend((item['package'], item['test']) for item in historical['required_integration_tests'])
    require(len(expected) == len(set(expected)) == 32, 'historical 32-contract inventory changed')
    actual = [(item['package'], item['test']) for item in doc['contracts']]
    require(actual == expected, 'contract identity/order differs from historical manifest')
    names = {}
    for path in (ROOT / 'backend').rglob('*_test.go'):
        for name in re.findall(r'^func\s+(Test\w+)\(', path.read_text(), re.M):
            names.setdefault(name, []).append(path.relative_to(ROOT).as_posix())
    retained = restored = 0
    for index, item in enumerate(doc['contracts'], 1):
        require(item['id'] == f'HDB-{index:02}', 'contract ID sequence changed')
        require(item['expected_runs'] == 1 and item['allowed_skips'] == [], 'acceptance policy changed')
        require(item['execution_status'] == 'NOT_RUN', 'inventory is not runtime evidence')
        path = item['current_path']
        require(names.get(item['test'], []) == ([path] if path else []),
                f'test presence changed: {item["test"]}')
        require(path is not None, 'all historical contracts must remain present')
        require(item['disposition'] == ('restored' if index <= 26 else 'retained'),
                'historical disposition changed')
        retained += item['disposition'] == 'retained'
        restored += item['disposition'] == 'restored'
        bound(path, item['test_sha256'])
        bound(item['fixture_path'], item['fixture_sha256'])
        for fixture in item['additional_fixture_inputs']:
            bound(fixture['path'], fixture['sha256'])
        tag = re.search(r'^//go:build (.+)$', (ROOT / path).read_text(), re.M)
        require(item['lane'] == (tag[1] if tag else 'untagged'), 'build lane mismatch')
    require(doc['coverage'] == dict(historical_contracts=32, present=retained+restored,
            retained_from_previous_baseline=retained, restored_since_previous_baseline=restored,
            missing=0, replaced=0), 'coverage summary differs from inventory')
    require(doc['gate_status'] == 'BLOCKED', 'inventory alone cannot release G0')
    ci = doc['current_ci']
    bound(ci['workflow'], ci['workflow_sha256'])
    bound(ci['critical_list'], ci['critical_list_sha256'])
    print(f'INT-00 inventory valid: 32 historical contracts; {retained+restored} present ({retained} retained, {restored} restored), 0 missing. G0 BLOCKED; no runtime tests executed.')


if __name__ == '__main__':
    try:
        validate()
    except (ValueError, KeyError, OSError) as exc:
        print(f'INT-00 inventory invalid: {exc}', file=sys.stderr)
        sys.exit(1)
