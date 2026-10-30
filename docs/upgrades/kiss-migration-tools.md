# Opt-in migration commands and startup ledger validation

Selective source: KISS commit `3a15e0c82d5795c45df8f9be85c0f2da9791cd61`.
Only migration tooling is carried forward. No profiling endpoint, Docker/resource
setting, logging policy, deployment workflow or deployment example is changed.
All existing SQL filenames and bytes remain unchanged, including both distinct
247 files and the custom refund retry index/affiliate claim pairing.

## Explicit operations

- `server --check-migrations`: opens PostgreSQL only, reads the full-filename
  `schema_migrations` ledger and exits nonzero for a missing/unreadable ledger,
  pending embedded migration, duplicate filename or incompatible checksum.
  This does **not** create/repair the ledger, Atlas records, indexes or tables.
- `server --migrate-only`: opens PostgreSQL only, invokes the existing migration
  runner and exits. Its existing advisory locking, ordering, transactional and
  nontransactional semantics are unchanged. This command **does apply DDL** and
  requires the operator's separately authorized migration credentials/window.
- Both commands return before setup/auto-setup, application wiring, Redis, workers,
  secret bootstrap or seeders. They reject combinations with one another or
  `--setup`/`--version`. Database work has a finite ten-minute context deadline.
  Errors are classified without printing database credentials or raw DSNs.
- `database.migration_mode: validate` (equivalently
  `DATABASE_MIGRATION_MODE=validate`) explicitly opts the **startup migration
  phase** into the same read-only ledger check. This is not a wholly read-only
  application startup: existing subsequent bootstrap/seeders and ordinary runtime
  database writes remain unchanged. Invalid modes fail before connecting.
- The omitted/empty/default mode remains `apply`, preserving ordinary startup.
  The explicit CLI operation takes precedence over this startup-only setting.

## Limits and operational interpretation

A green check is a snapshot of ledger correspondence, **not a full schema audit**:
manual schema drift, index validity or data invariants are not inspected. Checksums
use `strings.TrimSpace` exactly like the existing runner, and only the same named
historical compatibility exceptions are accepted. Extra historical ledger rows
are tolerated, matching the apply runner. Numeric prefixes are not identities.
Concurrent schema changes after the SELECT are outside that snapshot guarantee.

No live database is contacted by this implementation task. Any actual deployment,
migration execution, credential/privilege change or rollback needs a separate
operator decision. Existing payment worker/reconciliation and scoped-key rollback
constraints continue to apply. A readonly check role needs CONNECT, schema USAGE
and ledger SELECT only; use a separate migration-capable role for `--migrate-only`.

## Regression evidence contract

Owned unit tests cover full filename identity, whitespace normalization, historical
checksum compatibility, missing/pending/drifted/unreadable ledgers, zero-write SQL
expectations, CLI conflicts/deadlines/redacted errors and default/validate modes.
A mandatory isolated PostgreSQL test creates a private disposable schema and a
NOLOGIN/NOSUPERUSER role with SELECT-only ledger grants, enforces transaction
read-only mode, proves writes are denied, then checks the real API and startup
mode dispatch. Cleanup removes the private schema and role. No operator DSN,
live service, host socket or privileged test container is used. The prior 29
mandatory integration contracts remain, with this test added as contract 30.

## Review repair: auto-setup cannot bypass validate

Ordinary startup now rejects `AUTO_SETUP=true`/`1`/`yes` together with effective
`database.migration_mode: validate` **before** calling `NeedsSetup`. This also
precedes its data-directory writability probe, database creation, DDL, admin
bootstrap, config/installation-marker creation and application logger files.
The policy reader uses the normal config-source search (`CONFIG_FILE`, otherwise
`DATA_DIR`, `/app/data`, current directory, `./config`, `/etc/sub2api`) and normal
environment-over-file precedence. Invalid policy/config reads fail closed with
redacted errors, without loading unrelated settings or generating secrets.

This deliberately rejects the combination even when setup markers already exist:
finish any authorized initial setup with the default `apply` mode, then disable
`AUTO_SETUP` before selecting `validate`. No setting is silently rewritten.
Default/empty `apply` and explicit `--setup`, `--migrate-only`,
`--check-migrations`, `--version` precedence are unchanged. Validation still only
makes the migration phase read-only; ordinary later application writes are not
prohibited.

The added mandatory startup contract invokes the actual `main` from its compiled
integration-test subprocess in the reviewed network-none namespace. It supplies
only synthetic loopback fixture credentials, a bounded deadline, an absent
synthetic database, and an empty data directory. Assertions require the explicit
rejection, no created database, no config/marker/other data files, and no password
in diagnostics. It covers environment and external config-file policy and checks
that explicit read-only CLI dispatch still takes precedence. The original
read-only-role ledger contract remains mandatory. The standalone PR now has 31
mandatory contracts; union with the independent #10 contract has 32 (not a silent
replacement of the previously reviewed 31-contract union).
