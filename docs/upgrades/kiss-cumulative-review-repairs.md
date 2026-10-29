# Local cumulative review repairs

Base: actual validation PR #14 `3e6ba845fcb804d95c3f34eedfa90520e16d7089`.
This is validation-only composition, not an additional production merge path.

Reviewed local repair parents, merged with true ancestry:
- #8 `f8b5b8942e6e7b55dbee3a1c9f8129acacc35f84`: first-reader raw and decoded endpoint caps; preserve configured identity limits and global compressed ceiling.
- #9 `3af5e482`: explicit 429/402 account-failure policy precedence.
- #11 `cb540c8d`: reject validate plus required automatic setup before writes.
- #12 `d85340c6`: drain bare error until authoritative terminal usage; includes #9.
- #13 `b5169a39`: official OAuth endpoint policy and validated-IP shared-budget fallback.

## Reconciliation

Only production merge conflict was `httputil/body.go`. The resolution preserves
#7 strict default `MaxBytesReader` decompression via `readDecompressedRequestBody`
and retains #8's endpoint-specific reader with the minimum of caller cap and
64MiB compressed expansion ceiling. Identity inputs retain their selected endpoint
limit, not an accidental 64MiB restriction. Exact EOF/checksum checks remain.

All old combined interaction tests and #7 tests remain unchanged. Added a small
first-reader composition matrix for known/unknown length, exact raw/decoded caps,
oversize-before-duplicate precedence, same-value duplicates, and checksum failure
at the decoded cap; rejected requests never reach downstream admission.

The manifest keeps the previous 31 mandatory database contracts plus the new
startup validate/auto-setup contract: **32 total**. Parser and probe adapters are
both retained. Existing SQL filenames/bytes and workflows are unchanged. #7 and
#10 source branch references have not been rewritten.

No backend execution or runtime success is claimed by this local preparation.
Lead owns final source/restoration proof, individual red-to-green evidence, public
Draft publication and strictly network-isolated cumulative CI. No host Go tests,
Docker execution, live probes, deployment or PR merge occurred here.
