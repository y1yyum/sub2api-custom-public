# Stable v0.2.19 fixture transition

- Historical content baseline `.16`: `01842576b3d5c1e2545b5a99ad555d36e28ed306`.
- Upgrade baseline `.18`: `ac1b8f1271be0d674fa6f074403c1410f30a3a81`.
- Target `.19`: `dece26153621959ccd7635814f4b2d08304e681a`.
- Current custom checkout reviewed: `9bef3d75b8d33a2462d1a3f5bea4f10b20182189`.

`content_base`, `custom_base`, 108 preservation paths, and immutable migration /
release hashes are deliberately not rebased. `upgrade_base` identifies the new
transplant boundary. Both `target` and `test_revision` select `.19`, with no test
overrides. The fetch helper accepts only the three exact commits above, retaining
`.16` and `.18` for historical adapter checks. Runtime source preparation still
allows only the explicit content baseline/target revision selection; it copies
exact tracked custom working-tree bytes and never replaces owned tests.

## Reviewed adapted-input differences

Of the 30 previously adapted paths, 28 are byte-identical to `.18`. Exactly two changed:

1. `backend/internal/server/api_contract_test.go`: the group response contract
   adds `image_count_billing: false`. Keep this new upstream contract assertion;
   the existing adapter still only adds the custom unified-status setting to two
   admin-settings fixtures.
2. `backend/internal/service/account_test_service_gemini_test.go`: adds the
   geminicli import and `TestGeminiDefaultTestModel`, with Google One model and
   mapping assertions and the API-key default-model assertion. Keep the entire
   new function verbatim, including `t.Parallel()`. The existing Gin adapter
   changes only the earlier stream test's constant mode setup ordering.

All 30 new hashes are pinned alongside historical input hashes. No adapter code
was changed or assertion weakened. The stale-scope fixture production hash is
still the exact **custom** `content_moderation.go`; upstream `.18` and `.19`
versions are identical to each other, so no new production delta warrants
changing that gate. Tests check both facts separately.

## Preserved boundaries and evidence

The revision-only update does not modify workflow, image, runtime preflight,
network namespace, offline compilation, production code or OAuth contract. The
new scoped-admin mandatory PostgreSQL closure is separately documented in
[its review](SCOPED_ADMIN_FIXTURE_REVIEW.md). The existing custom refund-retry integration test remains
owned tracked source and is copied byte-for-byte, not fetched or adapted.

Local checks are Python/static/source preparation only. They do not establish
Go compilation, backend unit/integration success, or runtime compatibility of
new `.19` tests. Those require new reviewed isolated CI evidence on the final
integrated commit. No host Go/backend/Docker execution is authorized or performed.
