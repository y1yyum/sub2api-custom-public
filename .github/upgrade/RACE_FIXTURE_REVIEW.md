# Isolated-run fixture corrections

These are test-only adaptations based on actual PR4 isolated runtime evidence.
They do not remove the race detector, skip tests, replace production code or
relax the read-only `/src` boundary. Input SHA256 values for both fixed upstream
revisions are recorded in `manifest.json`; preparation records adapter and
adapted-output hashes.

## Asynchronous log buffer

`TestGrokImportProbeFailureLogDoesNotIncludeErrorMessage` polls `bytes.Buffer`
while the scheduler's background goroutine writes through slog. The observed
race is between `Buffer.Bytes` and `Buffer.Write/grow`. Replace that test's
buffer type with a mutex-protected writer. `Bytes()` copies the backing slice
under the lock, and `String()` takes the same lock. All Eventually/Contains/
NotContains assertions and their original timeout remain unchanged.

## Asynchronous mock repository

`TestGrokOAuthHandlerQueryQuotaProbesUpstream` inspects the mock repository's
updates map while background observed-model sync calls `UpdateExtra`. Add a
mutex to the mock, copy submitted updates while holding it, and expose an
independent locked snapshot. The original NotNil assertion now reads that
snapshot. Request count, payload, header, token-redaction, model and response
assertions remain unchanged. No sleep or synchronous production behavior is
introduced to hide the race.

## Ent schema loader private scratch

`TestAuthIdentityFoundationSchemas` calls `entc/load`, which generates `.entc`
relative to cwd. A read-only source mount correctly rejects that write. Before
the unchanged loader and all original schema assertions, copy the backend
module into `t.TempDir()` and `t.Chdir()` to its `ent/schema` directory. Copy the
complete module so go.mod/go.sum, generated Ent code, mixins and local domain
imports remain consistent rather than constructing a weaker substitute schema.
Files retain exact bytes; symlinks and other non-regular entries fail copying.
Only the private copy is writable; the source mount is not changed. The test
is sequential and `t.Chdir` restores cwd automatically during cleanup.

The original complete schema assertion body is retained. Go/module operations
still occur solely within the separately reviewed network-less runtime, with
its pinned dependencies and offline toolchain settings.

## Validation status

- Static pinned transformations and original assertion/test-count retention:
  checked with `python3 .github/upgrade/test-race-fixtures.py`.
- No Go compiler, test binary, Redis, PostgreSQL or external request was run as
  part of authoring these fixes.
- Functional/race validation remains required through the reviewed isolated CI
  runner. Static checks are not evidence that races or schema loading now pass.
