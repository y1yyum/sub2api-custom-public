"""Run ci-scope's whole and selected packages from the backend directory."""
import argparse
import os
import re
import subprocess

REPOSITORY = './internal/repository'
OWNER = 'TestScheduledOwnerTopology'


def scoped(packages, pattern=None):
    test = ['go', 'test', '-tags=integration']
    selection = ['-run', pattern] if pattern is not None else []
    if REPOSITORY in packages and (pattern is None or re.search(pattern, OWNER)):
        packages.remove(REPOSITORY)
        subprocess.run([*test, '-run', f'^{OWNER}$', REPOSITORY], check=True)
        subprocess.run([*test, *selection, '-skip', f'^{OWNER}$', REPOSITORY], check=True)
    if packages:
        subprocess.run([*test, *selection, *packages], check=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('shard', choices=('owner', 'repository-rest', 'other-packages', 'scoped'))
    shard = parser.parse_args().shard
    test = ['go', 'test', '-tags=integration']
    if shard == 'owner':
        subprocess.run([*test, '-run', f'^{OWNER}$', REPOSITORY], check=True)
    elif shard == 'repository-rest':
        subprocess.run([*test, '-skip', f'^{OWNER}$', REPOSITORY], check=True)
    elif shard == 'other-packages':
        listing = subprocess.check_output(
            ['go', 'list', '-tags=integration', '-f', '{{.Dir}}', './...'], text=True)
        packages = ['./' + os.path.relpath(directory) for directory in listing.splitlines()]
        packages.remove(REPOSITORY)
        subprocess.run([*test, *packages], check=True)
    else:
        scoped(os.environ['PACKAGES'].split())
        scoped(os.environ['SELECTED'].split(), os.environ['RUN'])
