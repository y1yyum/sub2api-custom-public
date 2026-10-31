#!/usr/bin/env python3
"""Rebuild .gitea/slow-unit-tests.txt from `go test -tags=unit -json ./...` on stdin.

Lists the top-level tests taking at least MIN_SECONDS in packages where such
tests add up to at least MIN_PACKAGE_SECONDS; .gitea/unit-full.sh runs them
in parallel shards beside the rest of their package.
"""
import json
import sys
from collections import defaultdict

MODULE = 'github.com/Wei-Shaw/sub2api/'
MIN_SECONDS = 1.0
MIN_PACKAGE_SECONDS = 30.0
HEADER = """\
# Slow unit tests: package, top-level test, seconds in the profiled run.
# .gitea/unit-full.sh runs these in parallel shards beside the rest of their
# package during full unit runs; nothing here is skipped. Regenerate with
#   go test -tags=unit -json ./... | python3 ../.gitea/slow-unit-tests.py > ../.gitea/slow-unit-tests.txt
# A renamed or deleted test simply runs with the rest of its package.
"""

slow = defaultdict(dict)
for line in sys.stdin:
    if not line.startswith('{'):
        continue
    event = json.loads(line)
    test = event.get('Test', '')
    if event['Action'] in ('pass', 'fail') and test and '/' not in test and event['Elapsed'] >= MIN_SECONDS:
        slow[event['Package'].removeprefix(MODULE)][test] = event['Elapsed']

sys.stdout.write(HEADER)
for package in sorted(slow):
    if sum(slow[package].values()) >= MIN_PACKAGE_SECONDS:
        print()
        for test, seconds in sorted(slow[package].items(), key=lambda item: (-item[1], item[0])):
            print(f'{package} {test} {seconds:.2f}')
