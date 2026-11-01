# INT-03: Antigravity Flash thinking budget

## Scope and compatibility

Base: `f347674975c1bfae5a38c60eed43fdda164fc1de` (main, checked 2026-11-01).
Reference: Kedaya commit `0f4db7fcc733ea471afcb91528070c9889e2c4ff`.
This is an adapted guard, not an upstream file replacement.

- Retain the existing case-sensitive `gemini-2.5-flash` substring guard.
- Also recognize canonical `gemini-<numeric version>-flash[-suffix]` IDs,
  case-insensitively, including the current 3/3.1/3.6/3.7/3.8 catalog names.
- Cap only positive budgets above 24,576. Preserve current zero/negative/missing
  budget normalization to the dynamic `-1` sentinel, disabled thinking, Opus
  adaptive behavior, and output-limit padding/ceiling rules.
- Do not cap arbitrary custom model names containing `flash`, Pro models,
  `flashlight`, or `pro-flash`. Prefixed newer IDs such as `models/gemini-3.8-flash` are not matched unless
  mapped to a canonical ID. Custom aliases are resolved by the existing
  transformer before this guard; tests exercise mapped-model selection.
- Preserve the exported constant name and caller request immutability.

No paid provider request, production read, deployment, migration, dependency,
workflow, security exception, or feature flag changes are included. Revert this
patch to restore the prior guard. No schema rollback is necessary.

## Verification

The new fixture failed against the unchanged base transformer: newer Flash IDs
kept 24,577/32,768 instead of 24,576; output padding used the uncapped budget.
The same fixture passed after the guard change. This demonstrates a local
request-shape defect, **not** a measured production 400 rate or proof that a
real provider now accepts every request.

Fixture: `backend/internal/pkg/antigravity/request_transformer_flash_test.go`.
Includes 18 models × 8 budgets × enabled/adaptive modes, disabled/missing
thinking, low output limits, Opus compatibility, serialization and mapped-model
controls. Local package run: 4 top-level tests (including existing OAuth test),
27 passing test events including subtests, zero skips.

Commands (Go 1.27.3, fresh test execution):

```sh
cd backend
go test -tags=unit -count=1 -json ./internal/pkg/antigravity
go test -race -tags=unit -count=1 -json ./internal/pkg/antigravity
go vet ./internal/pkg/antigravity
```

The official toolchain archive was checked against the SHA-256 from
`https://go.dev/dl/?mode=json`:
`65efbc80fdc1fa2cd2631cad5b481777a68e8725f34e89f50fc6ac714e379b40`.
Local module/build caches live in temporary workspace storage. No real provider
credentials or requests are required. Local execution is not a verified
network-none container and must not be presented as that stronger guarantee.

PR CI scope selects the whole Antigravity unit package plus current critical
unit tests and lint. It does **not** run the historical DB contract closure or
establish G0. Integration/real-PG settlement and full retained-suite results
must be reported separately; a green scoped `ci-gate` does not establish them.

Final local checks: package unit, package race and vet **PASS**; no skips.
Fixture SHA-256: `1e3c517a6cfcae2285f560fb6dc3b00feda56523a99df6f96bf889f08dd9b2b2`.
Real-provider validation: **NOT_RUN**, requires separate authorization.
