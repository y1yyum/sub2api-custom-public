# Stable v0.2.19 content integration (Draft)

## Exact scope

- Upstream baseline .18: `ac1b8f1271be0d674fa6f074403c1410f30a3a81`.
- Upstream target .19: `dece26153621959ccd7635814f4b2d08304e681a`, annotated
  release tag `e60cefeaf78f2fbfa95aa076029e28b40d0a5527` (`v0.2.19`).
- Reviewed custom main: `9bef3d75b8d33a2462d1a3f5bea4f10b20182189`.
  The earlier three-stage upgrade has been merged by the user; this new PR bases
  on main, not the frozen PR4/5 verification-history branches.

The 259-path upstream delta is reconciled in
`.github/upgrade/reconciliation-019.json`. Upstream tests are restored from the
exact target into an external validation overlay; owned custom regression tests
remain authoritative and are not overwritten. Sponsor-only README changes and
six partner assets are intentionally omitted. Custom branding, UI, release and
runtime OAuth injection remain. `backend/cmd/server/VERSION` stays custom0.3.1;
the .19 tag itself contains upstream0.2.18 fallback text, which is not copied.

## Functional groups and review risks

- Scoped administrator keys: complete repository/service/middleware/routes/UI
  closure, action/resource restrictions, expiration, IP allowlists, digest-only
  storage and retained revocation history. Keys require explicit authorized
  creation; no key or operator credential is supplied by this change.
- Legacy administrator key rotation/deletion requires step-up; the settings UI
  no longer offers initial legacy full-access-key creation. Preserve custom
  unified-status settings and documentation links while importing this policy.
- Image-count billing is opt-in, default false, for dedicated OpenAI/Composite
  image endpoints. Gemini fileData images and token usage accounting fixes are
  included; test actual counts, zero/invalid output and endpoint exclusions.
- Gemini default catalog/model mappings and same-account retry policy, DeepSeek
  pricing/cache/developer-role compatibility, Grok catalog/diagnostics/model
  observations, multipart oversize rejection and pagination/admin error fixes.
- Dependency versions already match the reviewed .19 security graph. Adopt the
  upstream pruned go.sum without reverting the x/net0.62 security correction.

## Migration compatibility and operational gates

Keep **both** distinct247 filenames unchanged:

1. `247_payment_audit_refund_retry.sql` (existing custom migration).
2. `247_scoped_admin_api_keys.sql` (new upstream migration).
3. `248_group_image_count_billing.sql` (new upstream migration).

The actual runner keys checksums by full filename and sorts full filenames, not
by numeric prefix. Do not rename/rewrite an applied custom247. Its partial unique
index and the paired PostgreSQL affiliate ON CONFLICT predicate remain intact,
as do both owned refund regression tests and the user's verification script.
New regression tests check the full embedded migration ledger and a rollback-only
private-schema simulation of new migrations over an already-custom247 schema.
No live migration is performed by this work.

Before any deployment: back up the database; inspect existing full-filename
migration/checksum history; follow the prior246 disabled-group capture/restore
runbook; preserve the payment worker freeze, reconciliation and no-old-worker
mixing gates from the .18/refund-retry runbooks. New scoped credentials must not be
assumed enforced by older binaries; do not roll back to old auth code while scoped
keys remain active. Do not drop key/audit tables or refund history as a rollback
shortcut. Revoke scoped keys and review operational policy before a planned binary
rollback. Re-enable image billing only by an explicit administrator decision.

## Validation contract

All backend execution remains in reviewed nonroot, read-only, capabilities-dropped
network-none containers. Compilation is offline and compile-only; no tests run in
networked image preparation. No host socket, host service, inherited real secrets
or external DSN is permitted. Fixed disposable PostgreSQL/Redis fixtures only.

New upstream scoped-admin PostgreSQL tests would otherwise skip without an
operator DSN. Pinned adapters move their database-dependent closure into the
mandatory integration lane using the existing loopback fixture; original pure
unit assertions remain. Required named integration tests must each run and pass
exactly once, with no skip, failure or duplicate. Source/compile/binary/runtime
receipts and actual service registry coverage remain fail-closed.

Actual candidate CI/artifact results are recorded in the Draft PR, not inferred
from older green runs or this plan. External/live-provider/plugin opt-in tests
remain explicitly outside covered execution. No approval, merge, release or
deployment is performed.
