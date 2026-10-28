# Stable v0.2.18 staged integration

> **Backend regression execution is quarantined (2026-10-28).** The unit and
> integration matrix jobs deliberately fail at their first step, before setup
> or test execution. This is a blocking failure, not a skip and not a test pass.
> Existing build/vet/vulnerability checks, frontend jobs and static preservation
> checks remain active. Do not manually run the backend commands below while
> this quarantine is in effect.

This series transplants content, not Git ancestry. The public custom repository
is a snapshot. Production source always comes from the PR checkout.

| Role | Fixed commit |
| --- | --- |
| Content baseline v0.2.16 | `01842576b3d5c1e2545b5a99ad555d36e28ed306` |
| Target v0.2.18 | `ac1b8f1271be0d674fa6f074403c1410f30a3a81` |
| Reviewed custom v0.3.1 | `357a6f0f5bd6cadc96d1caec19aeb8da52eeb55b` |

No upstream main-only feature, release, deployment or version bump is included.
The initial PR establishes safeguards; it does **not** implement v0.2.18.

## Reproduce

Use Go exactly as specified by `backend/go.mod`, pnpm **9.15.9**, and Node
20 (existing CI) or 24 (container build). Docker is needed for integration
fixtures. CI compiles sequentially with bounded GC/concurrency because the large
generated Ent package can exhaust runner memory during parallel race builds. Do not lower the required compiler version to get a green check.

```sh
python3 .github/scripts/test-upgrade-preservation.py
python3 .github/scripts/prepare-upgrade-validation.py /tmp/sub2api-validation-unique
cd /tmp/sub2api-validation-unique/backend
go mod verify
GOMAXPROCS=2 GOGC=30 go test -p 1 -race -count=1 -tags=unit -timeout=20m ./...
docker info
CI=true GOMAXPROCS=2 GOGC=30 go test -p 1 -race -count=1 -tags=integration -timeout=20m ./...
cd ../frontend
pnpm install --frozen-lockfile
pnpm run lint:check
pnpm run typecheck
pnpm run test:run
pnpm run build
```

The destination must not exist and must be outside the source repository. The
script copies tracked **working-tree bytes** (including local edits) and file
modes, then adds pinned upstream tests, testdata, testutil, Vitest setup and
lint configuration. It never replaces production files or repository-owned
regression tests. Commit new local test files before preparing, or stage them
so `git ls-files` includes them. `validation-receipt.json` records revision,
local diff summary and each restored path. Do not test a pristine upstream
checkout and present that as custom application coverage.

`manifest.json.test_revision` starts at v0.2.16 so the baseline can be checked
before changing behavior. Each coherent implementation stage must advance its
changed tests using `test_overrides` (path → full target SHA); the completed
series must use the target SHA globally. Overrides also express target-deleted
tests: an overridden path missing at the target is not restored from baseline.
Reviewed custom-contract adaptations live in `adapt-tests.py`. The manifest
pins SHA256 of each eligible .16/.18 input; unexpected bytes fail preparation.
The receipt records original/adapted hashes and the adapter's own hash. OAuth
URL/scope tests (including the Gemini service authorization-URL matrix) use
synthetic runtime credentials instead of built-in values;
missing credentials are asserted to fail closed. Admin-settings JSON fixtures
explicitly include the custom unified-status boolean. The frontend adapter
retains branding link checks, expands admin redirects across backend modes, and
adds unified-status/classic-override scenarios while retaining V1/V2 coverage. No production source is
changed by this adaptation.

Do not omit failing tests. Custom behavior needs an explicit regression test
and a reviewed adaptation if an upstream assertion no longer applies.

Two deliberately removed standalone CLIs (`cleanup-ingress-reject-logs` and
`profit-preview`) have no production package in the public snapshot. Their
orphan tests are explicitly excluded in the manifest, with a guard that fails
if production code reappears without restoring coverage. No shipped backend or
frontend package is excluded. `backend/ent/enttest` is restored as test support.

The full test dependency closure is restored, not just this release's changed
test files. Python preservation checks guard 108 custom-only paths, all existing
SQL migration bytes, release identity, the executable entrypoint and unified
settings. A tracked Go subprocess test checks OAuth startup with empty and
synthetic injected credentials; no real credentials are needed. Static checks
are not substitutes for runtime tests or browser QA.

## Stages and release blockers

1. Baseline and executable validation harness (this PR).
2. Coherent gateway/config/model/price/quota/cache changes, matching tests, and
   the image-group migration plus disabled-state capture/restore gate.
3. Complete payment/refund chain; never mix old/new workers.
4. Delivery rehearsal, deployment-specific approval and version decision.

All pre-existing SQL files retain their names and bytes. New upstream
`246_enable_gemini_image_generation_groups.sql` can coexist with custom
`246_random_user_id_allocation.sql`; migration identity is the full filename.
The new migration re-enables all false Gemini/Antigravity image switches,
including intentionally disabled groups. Before any future deployment, capture
disabled group IDs, approve the policy, and restore intended disables before
traffic. This series does not operate on a live database.

New pending refunds hold deductions, unlike the old worker. Before deployment:
freeze/converge workers, inventory pending refunds, reconcile provider records,
exercise old/new audit compatibility, and handle old Alipay query-unsupported
records manually. Rollback is **not** simply replacing an image or restoring a
DB snapshot: neither reverses provider-side payments. Freeze, reconcile, then
choose forward repair or coordinated restoration.

Release remains blocked on real-data sanitized migration rehearsal, payment
reconciliation, Redis compatibility, portal/browser scenarios and both CPU
architectures. A passing CI cannot replace those checks. Keep custom VERSION
0.3.1 until a separate release/version approval. Do not merge or publish images
from this draft integration series.

## Known inherited test limits

The upstream repository integration harness can skip a local Docker-less run;
use the explicit `docker info` preflight and `CI=true` above. CI requires Docker
rather than accepting a skipped suite. Upstream also already marks
`ConcurrencyCacheSuite.TestGetAccountsLoadBatch` as skipped (see the pinned
`concurrency_cache_integration_test.go` TODO near line 525). This series does
not add that skip or claim the corresponding behavior has passed. Track that
coverage gap separately; restored tests and passing jobs are not proof of zero
skips or complete end-to-end deployment coverage.

## Historical unrestricted-backend execution quarantine

A local full-unit-suite operation was rejected by execution safety review for a
potential external Google Cloud request. The rejected action was an output poll
of an existing suite; neither the exact originating test nor actual data
transmission has been established. Endpoint-looking strings can occur in local
proxy fixtures, so this is not a claim that credentials were leaked. Until the
transport path is proven safe, both backend-regression matrix jobs exit nonzero
before checkout/setup/tests. No test selection, assertion or build tag is
removed to produce a misleading green result.

Removal conditions (all required):

1. Identify the implicated test/request or otherwise establish the complete
   relevant transport path; distinguish a logical URL from the actual socket
   destination. Use source/fixture evidence without exposing credentials.
2. Demonstrate that the relevant unit tests use synthetic credentials and
   controlled localhost/mock transports, or obtain specific user authorization
   for any intentionally external request and its data. Do not silently convert
   a unit test to a live service test.
3. Review any missing mock/dependency correction while retaining assertions;
   lock adapted fixture inputs and record the changes. Do not suppress failures.
4. Obtain explicit approval for a narrowly scoped validation using the reviewed
   evidence. A denial must not be retried indirectly or via another executor.
5. After the approved isolation check, remove this guard in an independently
   reviewed change, then rerun the actual applicable unit/integration/race
   suites and report their exact results. Until then this PR is not ready.

The guard is an unconditional first step of both matrix jobs; it has no
continue-on-error, success fallback or conditional bypass. This temporary
quarantine does not broaden authority to contact external providers, publish a
release, migrate a live database or merge a PR.

## Approved isolated unit follow-up

The user explicitly approved continuing with a separate isolated-test PR. Unit
execution now has only one path: the fail-closed runner described in
[the isolation boundary](../isolation/README.md). Its host and in-container
preflights must pass before any test code runs. This is a materially different
network-none operation, not authorization to retry real cloud requests. The
original unrestricted commands above are historical reference, **not an approved
execution path**. Integration remains explicitly quarantined pending separate
database/Redis isolation. Receipt and actual CI outcomes, not this plan, establish
which tests ran and passed.

## Isolated integration follow-up

A separately reviewed PostgreSQL/Redis fixture lane now uses the same verified
network-none boundary. Fixed synthetic loopback databases are newly initialized
inside the test container; Redis uses per-test local processes/private Unix
sockets. No Docker daemon/socket, host service or operator DSN is exposed to
tests. The six prompt-audit database tests are explicitly integration-tagged,
not silently skipped. See [fixture contract](INTEGRATION_ISOLATION.md). Both
lanes fail on missing isolation/fixture evidence; neither is declared passing
until its exact CI artifacts establish execution and outcomes.
