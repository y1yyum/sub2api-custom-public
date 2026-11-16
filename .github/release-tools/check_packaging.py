#!/usr/bin/env python3
"""Check release notice selection, generated contexts and Docker COPY inputs."""
import argparse
import fnmatch
import io
import json
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile

import release_matrix as release

NOTICES = ('LICENSE', 'COPYING', 'NOTICE.md',
           'backend/internal/upstreamsites/LICENSE',
           'backend/internal/upstreamsites/COPYING',
           'backend/internal/upstreamsites/THIRD_PARTY_NOTICES.md')


def check_archives():
    for name in NOTICES:
        if not Path(name).read_bytes():
            raise ValueError(f'empty notice: {name}')
    archives = release.config()['archives']
    if not archives:
        raise ValueError('release must define at least one archive')
    for archive in archives:
        patterns = archive['files']
        for name in NOTICES:
            if not any(isinstance(pattern, str) and fnmatch.fnmatchcase(name, pattern)
                       for pattern in patterns):
                raise ValueError(f'archive omits {name}')


def license_copies(dockerfile):
    # Only the final image stage delivers notices to users.
    runtime = dockerfile.read_text(encoding='utf-8').rsplit('\nFROM ', 1)[-1]
    lines = [line for line in runtime.splitlines()
             if line.startswith('COPY ') and '/app/licenses/' in line]
    expected = {
        'COPY LICENSE COPYING NOTICE.md /app/licenses/',
        'COPY backend/internal/upstreamsites/LICENSE backend/internal/upstreamsites/COPYING '
        'backend/internal/upstreamsites/THIRD_PARTY_NOTICES.md /app/licenses/upstream-sites/',
    }
    if set(lines) != expected:
        raise ValueError(f'{dockerfile}: runtime license COPY contract changed')
    return lines


def check_contexts(directory):
    inputs = directory / 'inputs'
    inputs.mkdir()
    version, sha = '0.0.0', '0' * 40
    for target in release.targets():
        name = release.archive_name(version, target)
        path = inputs / name
        if target['goos'] == 'linux':
            with tarfile.open(path, 'w:gz') as archive:
                binary = tarfile.TarInfo('sub2api')
                binary.size = 7
                archive.addfile(binary, io.BytesIO(b'fixture'))
        else:
            path.write_bytes(b'fixture')
        metadata = dict(version=version, sha=sha, target=target, archive=name,
                        sha256=release.sha256(path))
        (inputs / f"manifest-{target['goos']}-{target['goarch']}.json").write_text(json.dumps(metadata))
    output = directory / 'contexts'
    release.contexts(argparse.Namespace(input=str(inputs), output=str(output),
                                       version=version, sha=sha, simple=False))
    for arch in ('amd64', 'arm64'):
        for name in NOTICES:
            if (output / arch / name).read_bytes() != Path(name).read_bytes():
                raise ValueError(f'{arch}: context notice differs: {name}')
    return output


def check_docker(source, dockerfile, output):
    lines = license_copies(dockerfile)
    subprocess.run(['docker', 'buildx', 'build', '--network=none', '--progress=plain',
                    '--output', f'type=local,dest={output}', '-f', '-', str(source)],
                   input='FROM scratch\n' + '\n'.join(lines) + '\n', text=True, check=True)
    for name in NOTICES:
        target = output / 'app/licenses'
        if '/' in name:
            target /= 'upstream-sites'
        if (target / Path(name).name).read_bytes() != Path(name).read_bytes():
            raise ValueError(f'Docker notice differs: {name}')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--docker', action='store_true', help='also exercise COPY with Docker BuildKit')
    args = parser.parse_args()
    check_archives()
    for name in ('Dockerfile', 'Dockerfile.goreleaser'):
        license_copies(Path(name))
    with tempfile.TemporaryDirectory(prefix='release-packaging-') as temporary:
        directory = Path(temporary)
        contexts = check_contexts(directory)
        if args.docker:
            # A minimal source context still applies the real .dockerignore. No local secrets enter it.
            source = directory / 'source'
            source.mkdir()
            for name in (*NOTICES, '.dockerignore'):
                dest = source / name
                dest.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(name, dest)
            check_docker(source, Path('Dockerfile'), directory / 'source-output')
            check_docker(contexts / 'amd64', Path('Dockerfile.goreleaser'), directory / 'release-output')
    print('packaging: six notices selected; both contexts match; runtime COPY checked')


if __name__ == '__main__':
    main()
