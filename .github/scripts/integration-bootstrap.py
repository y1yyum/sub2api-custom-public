#!/usr/bin/env python3
"""Offline disposable PostgreSQL lifecycle, after the shared pure preflight.

Library for the reviewed integration entrypoint, never a standalone bypass.
The caller runs its complete integration suite in the callback and preserves
both the isolation receipt and the returned fixture receipt. No operator DSN,
image, role, port, executable or password is accepted as input.
"""
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

_spec = importlib.util.spec_from_file_location('shared_isolation', Path(__file__).with_name('isolated-unit-entrypoint.py'))
shared = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(shared)

PG_BIN = '/usr/lib/postgresql/18/bin/'
REDIS = '/usr/local/bin/redis-server'
DATABASES = ('sub2api_repository', 'sub2api_prompt_audit')
ROLE = 'fixture'
PASSWORD = 'fixture-only'  # Synthetic disposable fixture; never a production credential.
HOST = '127.0.0.1'
PORT = '55432'


def integration_preflight(source, manifest, expected_digest):
    doc = shared.preflight(source, manifest, expected_digest)
    shared.require(os.geteuid() == 65532 and os.getegid() == 65532, 'fixture UID/GID must be 65532')
    return doc


def run_with_postgres(source, manifest, expected_digest, run_tests, *,
                      inspect=integration_preflight, execute=subprocess.run):
    """Return (callback_result, fixture_receipt); cleanup errors fail the run.

    `inspect` and `execute` injection exists solely for pure Python mock tests.
    Production callers must use the defaults, not substitute an inspection.
    """
    # Absolutely no executable/version probe/initdb before the complete check.
    doc = inspect(source, manifest, expected_digest)
    env = shared.test_environment()
    env['SUB2API_ISOLATED_INTEGRATION'] = '1'
    pg_env = {**env, 'PGPASSWORD': PASSWORD, 'PGCONNECT_TIMEOUT': '3'}

    def command(argv, *, postgres=False, timeout=30):
        return execute(argv, env=pg_env if postgres else env,
                       cwd=source / 'backend', capture_output=True,
                       text=True, check=False, timeout=timeout)

    versions = {}
    checks = (
        ('go', [shared.GO, 'version'], r'go version go1\.27\.3 linux/(?:amd64|arm64)\n'),
        ('postgres', [PG_BIN + 'postgres', '--version'], r'postgres \(PostgreSQL\) 18\.1(?: \([^\r\n]+\))?\n'),
        ('redis', [REDIS, '--version'], r'Redis server v=8\.4\.\d+ [^\r\n]+\n'),
    )
    for name, argv, pattern in checks:
        result = command(argv)
        shared.require(result.returncode == 0 and re.fullmatch(pattern, result.stdout), 'pinned ' + name + ' executable version mismatch')
        versions[name] = result.stdout.strip()

    receipt = {'versions': versions, 'postgres_host': HOST, 'postgres_port': int(PORT),
               'databases': list(DATABASES), 'synthetic_role': ROLE,
               'postgres_ready': False, 'cleanup': 'not-started',
               'redis_lifecycle': 'per-test private Unix socket; no shared Redis daemon'}
    # Only the private /tmp tmpfs verified above is writable. Each run initializes
    # an entirely new cluster, never application data, a backup or a mounted DB.
    with tempfile.TemporaryDirectory(prefix='sub2api-postgres-', dir='/tmp') as directory:
        root = Path(directory)
        data = root / 'data'
        password = root / 'password'
        password.write_text(PASSWORD + '\n')
        password.chmod(0o600)
        start_attempted = False
        cleanup_ok = True
        try:
            result = command([PG_BIN + 'initdb', '-D', str(data), '--username=' + ROLE,
                              '--pwfile=' + str(password), '--auth-local=scram-sha-256',
                              '--auth-host=scram-sha-256', '--encoding=UTF8', '--no-locale'],
                             timeout=60)
            shared.require(result.returncode == 0, 'disposable PostgreSQL initialization failed')
            password.unlink()
            with (data / 'postgresql.conf').open('a') as config:
                config.write("\nlisten_addresses = '127.0.0.1'\nport = 55432\nunix_socket_directories = ''\nssl = off\n")
            start_attempted = True
            result = command([PG_BIN + 'pg_ctl', '-D', str(data), '-l', str(root / 'postgres.log'),
                              '-w', '-t', '30', 'start'], timeout=40)
            shared.require(result.returncode == 0, 'disposable PostgreSQL startup failed')
            for database in DATABASES:
                result = command([PG_BIN + 'createdb', '-h', HOST, '-p', PORT, '-U', ROLE,
                                  '--maintenance-db=postgres', database], postgres=True)
                shared.require(result.returncode == 0, 'disposable database creation failed')
                # Verify authenticated connectivity to each distinct database,
                # not only a listening socket (pg_isready is insufficient).
                result = command([PG_BIN + 'psql', '-X', '-h', HOST, '-p', PORT, '-U', ROLE,
                                  '-d', database, '-v', 'ON_ERROR_STOP=1', '-Atqc', 'SELECT current_database()'],
                                 postgres=True)
                shared.require(result.returncode == 0 and result.stdout.strip() == database,
                               'disposable database readiness failed')
            receipt['postgres_ready'] = True
            result = run_tests(doc, env, receipt)
        except BaseException:
            # Startup-only log: no application SQL has run. Bound output and
            # redact even our synthetic password; never emit the environment.
            if not receipt['postgres_ready']:
                log = root / 'postgres.log'
                if log.is_file():
                    with log.open('rb') as handle:
                        handle.seek(0, os.SEEK_END)
                        handle.seek(max(0, handle.tell() - 4096))
                        diagnostic = handle.read(4096).decode('utf-8', errors='replace')
                    print(json.dumps({'record_type': 'fixture_startup_diagnostic',
                                      'postgres_log_tail': diagnostic.replace(PASSWORD, '[synthetic-password]')}),
                          file=sys.stderr, flush=True)
            raise
        finally:
            if start_attempted:
                receipt['cleanup'] = 'stopping'
                # pg_ctl may time out after the server has actually started;
                # attempt stop even on startup/readiness/callback failure.
                try:
                    stopped = command([PG_BIN + 'pg_ctl', '-D', str(data), '-w', '-t', '15',
                                       '-m', 'fast', 'stop'], timeout=20)
                    cleanup_ok = stopped.returncode == 0
                except (OSError, subprocess.TimeoutExpired):
                    cleanup_ok = False
                forced_cleanup = not cleanup_ok
                if forced_cleanup:
                    try:
                        stopped = command([PG_BIN + 'pg_ctl', '-D', str(data), '-w', '-t', '5',
                                           '-m', 'immediate', 'stop'], timeout=10)
                        cleanup_ok = stopped.returncode == 0
                    except (OSError, subprocess.TimeoutExpired):
                        cleanup_ok = False
                receipt['cleanup'] = ('stopped-immediate' if forced_cleanup else 'stopped') if cleanup_ok else 'failed'
            else:
                receipt['cleanup'] = 'not-started'
            # Failure is never converted to a green test result. The host must
            # tear down the whole container even if a database cannot stop.
            shared.require(cleanup_ok, 'disposable PostgreSQL cleanup failed; container teardown required')
    return result, receipt
