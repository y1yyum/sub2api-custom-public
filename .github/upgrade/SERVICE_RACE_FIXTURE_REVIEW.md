# Service fixture race adaptations

Scope: four restored test files only; fixed `.16` and `.18` input SHA-256
values are recorded in `manifest.json`. No production file or workflow change.
These fixes respond to race-detector reports in the isolated PR5 compiled-unit
artifact, not to an unrestricted local backend run.

- `channel_monitor_checker_body_test.go`: handlers publish captured body/header/
  path under a mutex; tests read snapshots with copied map/header containers.
  Decoded nested values are immutable after publication. Response status defaults
  are request-local, not a shared-field mutation; routing uses the request path.
  Response/network work remains outside the lock, so concurrent requests remain
  concurrent.
- `grok_free_quota_gate_test.go`: observation getters share the stub writer's
  mutex. Call-count observation is not worker completion. Tests wait for the
  production in-flight markers to empty before reading completed cache results,
  forcing expiry, and teardown. Those markers are installed synchronously before
  launch and removed by the worker's deferred cleanup after stores and sweeping.
  Never manually delete a live worker marker or assign over a live `sync.Map`.
  Shared cache reset uses `Clear` after completion; per-instance workers are also
  drained at cleanup. Completion uses the existing Eventually cadence and fails
  the test if it times out; it is not a fixed sleep or a skip.
- `usage_cleanup_service_test.go`: dashboard recompute writes and counter reads
  share a mutex; original Eventually conditions/timeouts are retained.
- `auth_service_email_bind_test.go`: code-cache reads/increments and sent-email
  bookkeeping share a mutex. Get returns a value copy rather than allowing an
  Attempts pointer to escape the lock, matching a serialized cache boundary.
  Original nil/error behavior and assertions are retained.

## Validation boundary

`python .github/upgrade/test-service-race-fixtures.py` checks both pinned inputs
for all four paths, preserves every original assertion line modulo synchronized
accessor spelling, retains test counts and existing skips/sleeps, and checks the
completion/copy seams. This is static transformation validation only. It does
not establish successful Go compilation or race-free execution. Those claims
require a reviewed, externally disconnected isolated CI run. No Go, Docker,
backend test, or external request was executed while preparing these adapters.
