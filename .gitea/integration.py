"""Run ci-scope's whole and selected packages from the backend directory."""
import os
import re
import subprocess

REPOSITORY = './internal/repository'
OWNER = 'TestScheduledOwnerTopology'


def run(packages, pattern=None):
    if packages == ['./...']:
        listing = subprocess.check_output(
            ['go', 'list', '-tags=integration', '-f', '{{.Dir}}', './...'], text=True)
        packages = ['./' + os.path.relpath(directory) for directory in listing.splitlines()]

    test = ['go', 'test', '-tags=integration']
    selection = ['-run', pattern] if pattern is not None else []
    if REPOSITORY in packages and (pattern is None or re.search(pattern, OWNER)):
        packages.remove(REPOSITORY)
        subprocess.run([*test, '-run', f'^{OWNER}$', REPOSITORY], check=True)
        subprocess.run([*test, *selection, '-skip', f'^{OWNER}$', REPOSITORY], check=True)
    if packages:
        subprocess.run([*test, *selection, *packages], check=True)


if __name__ == '__main__':
    run(os.environ['PACKAGES'].split())
    run(os.environ['SELECTED'].split(), os.environ['RUN'])
