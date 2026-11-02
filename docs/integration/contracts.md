# INT-00 — integration contract baseline

Updated **2026-11-02** for `y1yyum/sub2api-custom-public`.

- Baseline commit: `bd6d0ea606abde5f0f85698124bd877e3c998f48`
- Baseline tree: `99e94c1f334c8738333fa43eed1f1046f3b04571`
- **G0: BLOCKED. This is an inventory, not a test-pass report.**
- No production access, provider calls, migrations against customer data, merge,
  release, deployment, CI-policy changes or audit-exception changes are included.

## Historical contract reconciliation

[`contracts.json`](contracts.json) enumerates all **32** historical mandatory
PostgreSQL/Redis contracts, without lowering the historical count. Identities come
from the concatenation of `test_reclassifications[].test_functions` and
`required_integration_tests[]` in `.github/upgrade/manifest.json`. That input is
SHA-256 bound, along with each current test and its fixture entry points.
These are selected fixture bindings, not a claim of a complete transitive Go,
migration, image or runtime dependency receipt.

| Disposition | Count | Meaning |
| --- | ---: | --- |
| Retained from previous baseline | 6 | Named source remains; runtime `NOT_RUN` |
| Restored since previous baseline | 26 | Named source restored by merged PR #30; runtime `NOT_RUN` |
| Present / missing / replaced | 32 / 0 / 0 | Static source presence only |

History is preserved in `contracts.json`: the 2026-11-01 snapshot at
`f347674975c1bfae5a38c60eed43fdda164fc1de` (tree
`40df4a2a4fd6971f20fea3e6fd7a7321c1093b89`) had 6 present and 26 missing.
PR #30 restored those 26 prompt-audit and scoped-admin contracts and their
helpers. This removes the source-absence finding, **not the runtime evidence
gap**. No restoration is implemented by this inventory PR, and no historical
preparation script is executed. G0 remains BLOCKED.

The six retained contracts are:

| ID | Test | Source under `backend/` |
| --- | --- | --- |
| HDB-27 | `TestUpgrade019CustomAndUpstreamMigrationLedger` | `internal/repository/upgrade_019_custom_migrations_integration_test.go` |
| HDB-28 | `TestUpgrade019AfterAppliedCustomRefundMigration` | same as HDB-27 |
| HDB-29 | `TestPaymentAuditLogRefundRetryConstraints` | `internal/repository/payment_refund_retry_integration_test.go` |
| HDB-30 | `TestAccountWindowStatsDistinctStartsPostgres` | `internal/repository/usage_window_distinct_start_integration_test.go` |
| HDB-31 | `TestCustomMigrationLedgerReadOnlyRole` | `internal/repository/migration_check_custom_integration_test.go` |
| HDB-32 | `TestCustomValidateAutoSetupStartupRejectsBeforeWrites` | `cmd/server/migration_startup_custom_integration_test.go` |

Each contract requires exactly one top-level run and pass in an acceptance run,
with no allowed skips. This is the integration-plan acceptance policy, **not a
claim that the current workflow enforces event counts**. Keep nested subtests
separate from top-level contract counts.

## Current CI and fixture boundary

The baseline public GitHub workflow is `.github/workflows/local-ci.yml`.
Its hash is bound for inventory only; no `.github` or `.gitea` workflow is modified.
PR #30 restored a larger test closure and updated `.gitea/critical-tests.txt`;
the manifest binds the new critical-list bytes. `.github/CI.md` still describes
the earlier trimmed snapshot and is not proof of current fixture behavior.
PR scope uses `.gitea/ci-scope.py`; package/full success cannot replace mandatory
contract event auditing. Neither race nor verified network-none execution is
established by this inventory. Go is pinned to **1.27.3**.

### Actual build lanes and fixture prerequisites

| IDs | Actual source lane | Fixture and skip/failure boundary |
| --- | --- | --- |
| HDB-01–06 | Untagged (also included in tagged runs) | `PROMPT_AUDIT_TEST_POSTGRES_DSN`; missing value skips even under CI |
| HDB-07 | Untagged | Both that PG DSN and `PROMPT_AUDIT_TEST_REDIS_ADDR`; missing either skips |
| HDB-08 | Untagged | Redis address only; missing skips |
| HDB-09 | Untagged | Both PG DSN and Redis address; missing either skips |
| HDB-10–26 | `unit`, despite real PostgreSQL | `pgtest.DSN`: explicit `SCOPED_ADMIN_TEST_DSN`, otherwise disposable `postgres:18.1-alpine3.23`; unavailable container skips outside CI, fails when `CI` is nonempty |
| HDB-27–31 | `integration` | Restored `integration_harness_test.go` package `TestMain` starts PG **and Redis**, even for PG-only tests; no Docker exits 0 without `m.Run` outside CI, exits 1 when `CI` is nonempty |
| HDB-32 | `integration` | Own startup PostgreSQL Testcontainer; startup errors fail, not skip |

HDB-27–31 no longer bind the deleted `public_integration_harness_test.go`.
Their restored harness defaults to `postgres:18.1-alpine3.23` and
`redis:8.4-alpine`; `SUB2API_TEST_POSTGRES_IMAGE` can override the PG image.
These are mutable tags, not verified image digests or actual server versions.
Prompt-audit endpoints are externally supplied and versions remain unverified.
The historical 18.1-bookworm / Redis 8.4 network-none design is not runtime proof.

Use **only disposable isolated test endpoints**, never a customer/business DSN:
prompt-audit fixtures create and truncate tables; scoped-admin fixtures create
and drop schemas. `pgtest` relies on the container reaper rather than explicit
termination. The repository harness registers deferred container termination,
but then calls `os.Exit`, which bypasses Go defers; cleanup is not proven.
Startup-fixture termination errors are ignored. Reviewers must record readiness
and actual cleanup, not infer either from helper presence.

The historical manifest's reclassification lane is not applied automatically:
HDB-01–09 remain untagged and HDB-10–26 remain unit-tagged in current sources.
Do not run only `-tags=integration` and claim all 32 passed, or count a package
exit 0 with no mandatory test events as success. For an acceptance run each
historical top-level test must run and pass exactly once, without skips;
reviewer lane selection must avoid counting untagged tests twice.

## Other retained P0 coverage and limits

- `backend/internal/handler/openai_custom_late_disconnect_test.go` covers ordinary
  Responses Write/Flush wrapper scenarios, but uses a mocked usage repository.
  It is not proof of real-PG exactly-once settlement or passthrough Flush coverage.
- `backend/internal/pkg/httputil/body_custom_compressed_boundary_test.go` and
  `body_endpoint_limit_test.go` retain decompression/checksum and endpoint caps.
- `backend/internal/pkg/probehttp/transport_owned_test.go` retains validated-IP,
  Host/SNI, redirect, HTTP CONNECT/SOCKS, cancellation/fallback/budget tests.
- HDB-29 covers refund retry audit retention and partial-index claim SQL. Do not
  extrapolate it to all full-service refund/concurrent settlement contracts.
- Existing tests are coverage candidates, not freshly passed evidence. New
  INT-01/02/03 contracts must be listed separately, not counted as historical 32.

## Review handoff and reproduction

Per the 2026-11-01 implementation direction, reviewers pull the Draft PR locally
and run acceptance in their Gitea CI (`.gitea/workflows/local-ci.yml` and the
reviewer-owned local execution setup). **Do not modify public-repository CI
workflows for this plan.** The current public workflow inventory below/above is
coverage context, not a request to change it or substitute public scoped checks
for reviewer acceptance. No Gitea execution access or result is claimed here.

Reviewer checklist: verify exact PR head/tree and fixture hashes; execute the
applicable local Gitea lanes; record run URL, tool/database versions, real test
events, skips, readiness and cleanup evidence; report unavailable contracts as
BLOCKED instead of inheriting green status from public scoped CI.

## Reproduction and evidence

Validate this **static inventory** from the repository root:

```sh
python3 docs/integration/validate_contracts.py
python3 docs/integration/test_validate_contracts.py
```

Maintenance verification on 2026-11-02: static validator PASS, 11 offline
validator tests PASS, `git diff --check` PASS. No Go, PostgreSQL, Redis,
container or network acceptance test was run for this refresh. The earlier
service-suite network denial is not retried by this documentation work.

The validator requires Python 3.9+ and uses only its standard library. It reads
local files and statically checks all
32 identities/presence/hashes/build-tag declarations and never invokes Go, Docker or external services.
Its declaration scan is not Go-aware executable test discovery; actual
`go test` events are still required for runtime acceptance.
Changed bound inputs require a reviewed inventory update; a validation failure is
not permission to remove historical contracts or weaken acceptance.

Existing broad command examples in `.github/CI.md` are listed for historical
context only, **not authorized execution or a complete acceptance recipe**:

```sh
cd backend
bash ../.gitea/unit-full.sh
go test -tags=integration ./...
golangci-lint run --timeout=30m ./...
```

These commands are **not executed by this document** and do not themselves
establish network isolation. For plan acceptance, run reviewed fixtures in the
approved isolated environment, capture uncached `-count=1 -json` results and race
coverage where required, and verify every mandatory run/pass/skip event. Record
candidate commit/tree, actual PG/Redis versions and image digests, fixture hashes,
commands, logs, source/artifact bindings, readiness and cleanup results. Report
`NOT_RUN`, `BLOCKED`, failures and skips separately from passes.

## Next dependencies and stopping conditions

1. Source restoration of all 26 formerly missing contracts is recorded via PR #30.
   Obtain exact-head runtime evidence for all 32 with the actual lanes above;
   source presence alone does not release G0.
2. Add valid INT-01 passthrough Flush-only and real-repository settlement coverage;
   demonstrate red/green only where a valid fixture actually reproduces a defect.
3. Complete INT-02 decompression, probe, refund and migration-entry matrices;
   keep unconfirmed pricing races classified as investigation.
4. INT-03 Flash remains an independently reviewable small patch with boundary,
   sentinel and non-target tests. No real provider verification is implied.
5. Obtain exact-head isolated/race/PG and cumulative evidence plus independent
   review before G0 release. Do not let subsequent P1 integrations take over
   execution while G0 is blocked. Merge/deploy and paid calls remain separately
   authorized actions.

Open publisher PR #23 is outside this work. Historical PRs #4/#5 remain audit
records, not additional implementation PRs to replay or merge.
