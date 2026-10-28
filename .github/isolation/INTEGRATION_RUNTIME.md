# PostgreSQL / Redis integration runtime candidate

This candidate is not authorization to run a database or suite on the host. It
contains an image definition and a lifecycle helper only. The reviewed host
runner, immutable-image verification and all shared isolation preflight checks
remain mandatory. No Docker, Go or database command was executed to validate this
candidate; its Python tests mock every subprocess.

## Image assembly and metadata boundary

The host must resolve official `postgres:18.1-bookworm`, `redis:8.4-bookworm` and
`golang:1.27.3-bookworm` to immutable digests and provide `PG_IMAGE`, `REDIS_IMAGE`
and `GO_IMAGE`. Record the resolved digests and resulting image ID in the run
receipt. Do not accept operator-controlled alternative images or architectures.

`integration.Dockerfile` uses the PostgreSQL Debian filesystem as its ABI base,
installs only OS dependencies, copies the pinned Go tree and Redis executable,
and downloads/verifies the exact Go module graph. None of those build steps
executes repository source, package initialization or tests. Repository source
is copied only after network-enabled build steps are over. ABI compatibility,
actual versions and runtime correctness still require the isolated CI run.

The final `FROM scratch` with `COPY --from=fixture_dependencies / /` copies the
filesystem but **does not inherit the PostgreSQL image's VOLUME metadata**.
A direct final `FROM postgres:...` could silently create an anonymous volume,
violating the private-tmpfs-only contract. The final stage declares no VOLUME,
entrypoint, ports, healthcheck or environment. Host image inspection must require
empty volumes and verify the exact immutable image ID before container creation.
Container inspection must also reject anonymous volumes and all host binds.

All processes share UID/GID 65532 and the same Docker `--network none` namespace,
read-only root, capability drop, no-new-privileges, resource bounds and private
`/tmp` tmpfs. No Docker socket or host credential mount is used. The fixture user
has a passwd/group entry so `initdb` can resolve its identity; this is not an
external database account. Existing unit isolation checks are not weakened.

## Bootstrap API

The integration entrypoint imports `integration-bootstrap.py` from the same
reviewed directory as `isolated-unit-entrypoint.py` and calls:

```python
result, fixture_receipt = bootstrap.run_with_postgres(
    source, manifest, expected_digest,
    lambda doc, env, fixtures: run_complete_integration_suite(doc, env, fixtures),
)
```

The helper first performs **all shared pure preflight checks**, including exact
source hashes, and additionally requires UID/GID 65532. Only afterward does it
execute version checks for Go 1.27.3, PostgreSQL 18.1 and Redis 8.4.x, recording the
full binary strings. The minor Redis tag follows the upstream fixture; the exact
patch and immutable image digest are preserved as evidence. No arbitrary parent
environment, DSN, role, port, executable or credential is accepted. The callback
gets a newly constructed offline environment plus the integration marker, not
`os.environ`. Synthetic `PGPASSWORD` is only passed to bootstrap DB client tools.

Every run initializes a new cluster in a mode-0700 temporary directory under
`/tmp`, with scram authentication, role `fixture`, synthetic password
`fixture-only`, TCP address **127.0.0.1:55432**, and no PostgreSQL Unix socket.
The password file is mode 0600 and removed immediately after initdb. Two separate
databases, `sub2api_repository` and `sub2api_prompt_audit`, are created and each
must pass an authenticated `current_database()` check before any test executes.
No production data, snapshot or existing cluster is reused.

Redis is not started by the bootstrap. The reviewed per-test fixture launches
`/usr/local/bin/redis-server` with TCP disabled and a fresh private Unix socket;
its own cleanup owns the process. It shares the same isolated namespace.

The callback's exit status is preserved. A finally block stops PostgreSQL even
when startup, readiness or the callback raises. Fast shutdown failure attempts
immediate shutdown; the receipt distinguishes `stopped` from `stopped-immediate`.
Failure of both modes raises an isolation error, never a pass. The host must
always destroy the container (including any residual process and private tmpfs)
on success, failure, timeout or interrupted execution. Print the terminal test
receipt only **after** this helper returns, including final fixture cleanup
status; missing terminal receipts and cleanup errors fail closed.

## Pure checks and remaining gates

```sh
python3 -m unittest discover -s .github/isolation -p 'test_integration_bootstrap.py' -v
```

These mocks verify ordering, versions, fixed environments, separate authenticated
readiness, failure propagation and cleanup. They do not establish actual image
ABI compatibility, process readiness, migration behavior, race safety or a
successful application suite. Those remain pending until the reviewed isolated
runner is explicitly enabled. Existing unrestricted execution denial is unchanged.
