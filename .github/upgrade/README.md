# Stable v0.2.18 staged integration

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
GOMAXPROCS=2 GOGC=30 go test -p 1 -race -count=1 -tags=integration -timeout=20m ./...
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
URL/scope tests use synthetic runtime credentials instead of built-in values;
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
