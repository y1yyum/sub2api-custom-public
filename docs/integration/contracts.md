# INT-00 — integration contract baseline

Recorded **2026-11-01** for `y1yyum/sub2api-custom-public`.

- Baseline commit: `f347674975c1bfae5a38c60eed43fdda164fc1de`
- Baseline tree: `40df4a2a4fd6971f20fea3e6fd7a7321c1093b89`
- **G0: BLOCKED. This is an inventory, not a test-pass report.**
- No production access, provider calls, migrations against customer data, merge,
  release, deployment, CI-policy changes or audit-exception changes are included.

## Historical contract reconciliation

[`contracts.json`](contracts.json) enumerates all **32** historical mandatory
PostgreSQL/Redis contracts, without lowering the historical count. Identities come
from the concatenation of `test_reclassifications[].test_functions` and
`required_integration_tests[]` in `.github/upgrade/manifest.json`. That input is
SHA-256 bound, along with each retained test and its current fixture entry point.
Missing current test/fixture hashes are `null`, not invented hashes.

| Disposition | Count | Meaning |
| --- | ---: | --- |
| Retained | 6 | Named source remains; runtime result is `NOT_RUN` in this inventory |
| Needs restoration or reviewed equivalent | 26 | Source absent; relevant production functionality remains present |
| Replaced | 0 | No equivalent replacement established by this audit |

The 26 missing contracts cover prompt-audit persistence/config/payload handling
and scoped-admin key/account/proxy transactions. Their absence is **not** proof
that the functionality was removed. Restoring their smallest valid fixture
closure, or documenting independently reviewed equivalent coverage, is required
before historical acceptance can be claimed. Do not blindly run the historical
preparation script against the current public snapshot.

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

The baseline public GitHub workflow is `.github/workflows/local-ci.yml`. `.github/CI.md` explicitly
marks `upgrade/`, `isolation/` and the old preparation scripts as historical.
That public CI tests the retained public snapshot; it does not fetch the former full
upstream test closure. At the baseline there are 44 Go test files and 15 frontend
test files. File counts are descriptive, not acceptance criteria.

- PRs use `.gitea/ci-scope.py` plus `.gitea/critical-tests.txt`.
- Manual/tag full runs mean all **retained** tests, not all historical contracts.
- `ci-gate` accepts out-of-scope skipped jobs; security remains always-on.
- Current commands do not enable race or a verified network-none runtime.
- Current workflow does not bind mandatory contract events to fixture/source
  receipts. Historical isolated-run receipts cannot be inherited by a new head.
- Go is pinned to **1.27.3** by `backend/go.mod` and workflow version checks.
- Current repository and startup fixtures use Testcontainers with
  **`postgres:18.1-alpine3.23`**, a tag, not an immutable image digest. They create
  disposable databases using synthetic fixture credentials; no business DSN is
  read. Docker/container startup failures fail the test rather than skip it.
- Container termination errors are currently ignored by these helpers; do not
  claim verified cleanup from their mere presence.
- The historical isolation design used PG **18.1-bookworm**, Redis **8.4**, and a
  verified network-none runtime. That design is not the current runtime.

For a transformer-only Flash PR, the current scope selects the antigravity unit
package, applicable critical **unit** tests, and package lint. It does **not**
select the DB integration critical tests, which depend on a DB-layer/critical-list
change. Its green CI therefore cannot establish G0.

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
```

The validator requires Python 3.9+ and uses only its standard library. It reads
local files and statically checks all
32 identities/presence/hashes and never invokes Go, Docker or external services.
Its declaration scan is not Go-aware executable test discovery; actual
`go test` events are still required for runtime acceptance.
Changed bound inputs require a reviewed inventory update; a validation failure is
not permission to remove historical contracts or weaken acceptance.

Current retained-suite commands, with the required toolchain/dependencies and
Docker available, are documented in `.github/CI.md`:

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

1. Restore/review the 26 missing historical contracts and their minimal helper
   closure. Maintain current runtime behavior and historical migration checksums.
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
