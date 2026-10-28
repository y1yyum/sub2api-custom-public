# Docker-free isolated integration fixtures

This follow-up wires a separate integration lane only through the reviewed
network-none runner and local fixture bootstrap. The old unrestricted command
remains prohibited. Static adaptation checks do not prove runtime isolation or
application correctness; actual CI receipts and results are still required.

## Runtime contract for the future runner

- One non-root Linux container contains PostgreSQL 18.1, Redis 8.4 and the pinned
  Go toolchain. The executable `/usr/local/bin/redis-server` must be available.
  Record actual versions and immutable image digest, not just mutable tags.
- Docker runtime enforces `--network=none`, `--cap-drop=ALL`,
  `--security-opt=no-new-privileges`, a read-only root, and bounded private tmpfs
  for `/tmp` and fixture state. No host networking, published ports, privileged
  mode, Docker/container sockets, host credentials, or host filesystem mounts.
- The reviewed bootstrap initializes a disposable PostgreSQL cluster as the
  same non-root user. It listens only at `127.0.0.1:55432`, with role `fixture`
  and **synthetic test-only** password `fixture-only`; databases are
  `sub2api_repository` and `sub2api_prompt_audit`. This cluster must be fresh for
  every run. Never reuse application data, backups or operator DSNs.
- Only the isolated runtime sets `SUB2API_ISOLATED_INTEGRATION=1`. The helper
  additionally rejects root, any non-loopback interface, Docker endpoints and
  known Docker sockets. These checks are defense in depth; the flag/checks are
  not themselves proof of the complete container security boundary.
- All compilation and test execution occurs inside that network-less runtime,
  with `GOTOOLCHAIN=local`, offline dependency resolution, immutable source and
  dependencies, and a cleared/allowlisted environment. Image assembly with
  networking may fetch pinned dependencies; it must never execute repository
  tests or repository-supplied build scripts.

## Six explicit fixture adaptations

1. `repository/integration_harness_test.go`: replace container creation,
   discovery and Docker availability skips with fixed local PostgreSQL and a
   private Redis child process. Preserve migration application, Ent setup,
   transaction helpers, Redis namespacing and test assertions. Return through
   a helper before `os.Exit` so database cleanup executes.
2. `middleware/rate_limiter_integration_test.go`: each `startRedis` call creates
   a new Redis process with a unique private Unix socket. TTL assertions remain
   unchanged; missing fixtures fail instead of skipping.
3. `server/routes/auth_rate_limit_integration_test.go`: same per-test Redis
   process isolation; registration threshold/status assertions remain unchanged.
4. `securityaudit/prompt_repository_integration_test.go`: replace arbitrary
   operator DSN with the separate prompt-audit database. Its minimal schema and
   TRUNCATE operations must never run against the repository database. Add the
   explicit `integration` build tag to its **six** DB-only tests, previously
   untagged and implicitly skipped when no DSN was provided. They remain
   mandatory in the integration lane; no assertion/test is deleted. The receipt
   lists all six moved test names, not a silent unit pass.

5. `securityaudit/prompt_config_integration_test.go`: move its one DB/Redis test
   into the integration lane, replace the operator Redis endpoint and implicit
   skip with a fresh private Unix-socket Redis fixture, retain CAS, encryption,
   invalidation and TTL assertions.
6. `securityaudit/prompt_payload_store_integration_test.go`: move its two Redis
   tests into the integration lane, use a fresh Redis process for each, retain
   payload TTL/namespace/delete and runtime metrics assertions. Missing fixtures
   fail; `PROMPT_AUDIT_TEST_REDIS_ADDR` is never read.

The manifest now names **nine** reclassified tests: six PostgreSQL tests plus
three Redis-dependent tests. This is explicit coverage transfer, not deletion or
an empty unit-suite pass. Redis setup/cleanup code changes; the remainder of each
test body and all business assertions are compared byte-for-byte by static tests.
The deliberate unavailable `127.0.0.1:1` client in the config degradation assertion
is retained inside the same network-none namespace.

`promptAuditUpdateRequest` and `integrationResult` are also used by untagged unit
tests. Their exact unchanged upstream bodies are moved into the hash-pinned,
untagged `securityaudit/isolated_shared_fixture_test.go` support file and removed
from tagged originals. This avoids undefined symbols in the unit lane without
reclassifying or weakening those unit tests. Preparation and host verification
allow this one exact securityaudit support path, not the entire package.

No test uses Docker/testcontainers after these lifecycle adaptations. Redis
opens **no TCP port** (`--port 0`); it uses `Network: "unix"` and a per-instance
socket under its private temporary directory. Persistence is disabled. Tests
register cleanup; startup cancellation/deadlines stop the child and remove its
data, and cleanup reports forced/unsuccessful termination. Redis instances are
not shared across the two rate-limit helper invocations or across packages.

## Source and fixture receipts

`manifest.json` pins every affected upstream file for both reviewed fixed
revisions. The adapter retains business assertions verbatim; only reviewed fixture lifecycle setup is replaced. The local helper
is stored under `.github/upgrade/fixtures` and copied only into the external
validation workspace, with its own SHA256. It is not added to shipped server
source. The preparation receipt records source hashes, adapter hashes,
adaptation before/after hashes, support-file hash and test reclassification.

Static review can be reproduced without starting Go, databases or network calls:

```sh
python3 .github/upgrade/test-isolated-fixtures.py
```

This check requires the already-fetched pinned Git objects; it does not fetch
anything. Actual fixture behavior, race checks, migration compatibility, process
cleanup and test execution remain **NOT RUN** until the isolated runtime is
reviewed and independently verified. Do not run the adapted backend directly
on a networked host or remove the existing quarantine to obtain a result.

## Remaining integration gates

Verify image ABI compatibility and actual PG/Redis versions; prove there are no
external network interfaces/routes and no host socket/credential access; verify
missing fixture failures and zero-test detection; capture expected versus
unexpected skips; run all applicable tests/race checks and separately report
inherited skips (including upstream `TestGetAccountsLoadBatch`). PostgreSQL 18.1
coverage is not evidence of compatibility with older deployed PG versions. A
future oldest-supported-version matrix must change only pinned fixture inputs,
not accept an arbitrary runtime image or DSN override.
