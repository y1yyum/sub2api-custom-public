# v0.2.19 scoped-admin PostgreSQL closure

Upstream labels 17 PostgreSQL-dependent tests `unit` and skips them when
`SCOPED_ADMIN_TEST_DSN` is absent. That is not valid security regression coverage.
The pinned adapter reclassifies their complete 11-file helper closure as
`integration`, including account/proxy selection, proxy sampling and the legacy
admin-key rotation race. The manifest lists each of the 17 names (repository 10,
service 6, admin handler 1) for mandatory runtime pass auditing. Missing, skipped,
failed, or duplicated required results must remain a blocking failure.

Six fixture setup sites now call existing
`isolatedfixture.PostgresDSN("repository")` and require success. There is no
operator DSN, new service/database, host socket, external interface, or privilege
change. The fixed DSN is a URL, so the existing generated unique schema name is
appended as `&search_path=...`, not a keyword-DSN suffix. The same per-test schema
DDL, transaction SQL, assertions and rollback checks remain. Each DROP SCHEMA
cleanup now asserts success rather than ignoring its error. All tests remain
inside the reviewed disconnected namespace and require its fixture preflight.

Four files also contain eight pure/mock tests. Their original functions are
copied verbatim into exactly two unit-tagged support files:

- `backend/internal/repository/isolated_scoped_admin_unit_test.go`: 3 tests.
- `backend/internal/service/isolated_scoped_admin_unit_test.go`: 5 tests.

The original mixed files run in integration, preserving their imports, helper
methods and test bodies. Thus these eight pure tests still run once in unit and
also run in integration; no unit test disappears and there are no duplicate
symbols within a build-tag lane. Support contents have manifest SHA-256 pins.
The source preparer and host allowlists must both accept only the two exact
paths, not a broader repository/service prefix. No testcontainer access is added.

Static checks verify all 11 input pins, original test names/assertion lines,
absence of operator-DSN skips, the 17-name manifest inventory, and byte-identical
unique copies of all eight pure tests. They do not prove compilation, import
closure, schema compatibility or runtime success. New isolated CI must establish
those properties on the final integrated source, including actual required PASS
records. No Go/backend/Docker execution was performed locally.

Separately, six previously user-owned refund-retry files gain additive immutable
hash protection, without changing the original 108-path preservation contract.
