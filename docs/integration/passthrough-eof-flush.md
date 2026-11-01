# INT-01b — EOF-residual FlushError result handoff

Stacked on [INT-01a / PR #27](https://github.com/y1yyum/sub2api-custom-public/pull/27),
parent commit `0d10589143f1577ddb4f42554e24101cb4462534` (the docs correction
to runtime-tested parent `ed24fc57463d3b220537f37771955c924815a6f4`).
This branch contains only the incremental EOF fix, test and note relative to #27;
review/apply the parent first. It does not duplicate the parent's implementation
as an independent main-based patch.

## Reproduced gap

A valid final JSON SSE data line can end at EOF without a blank event boundary.
The existing scanner forwards that line without inventing a blank line (its
concatenated-JSON repair is a different path). Passthrough previously left the
residual flush to a defer. `resultWithUsage()` had already copied the disconnect
flag before that defer observed FlushError, so:

- failed/nonterminal EOF could lose the usage result at the actual Forward
  error boundary;
- completed EOF retained usage but incorrectly reported no client disconnect.

The new fixture explicitly checks scanner output and exercises actual Forward
through Gin's genuine writer. The first output flush succeeds; the final event's
flush fails, while Writes continue succeeding and request context stays live.
The upstream closes naturally; no watchdog creates a successful result.

The 18-case matrix is 3 endings (`failed`, `completed`, `in_progress`) × 3 final
suffixes (no newline, one newline, normal blank boundary) × 2 transport states.
On the unchanged #27 source, six missing-boundary disconnected cases fail;
12 connected/normal-boundary controls pass. No malformed JSON is used.

## Narrow change and limits

After the existing post-scan cancellation observation, explicitly flush pending
output **before** final disconnect classification and result snapshot. The
existing flush guard prevents output after known cancellation or duplicate
flushes for normal boundaries. The deferred fallback remains in place.

This does not alter connected failure/nonterminal policy, output budgets,
provider retry decisions, pricing, schema, permissions, or public CI workflows.
It closes this post-scan EOF path, not every early return inside the scan loop.
Real handler/Ops capture plus PostgreSQL exactly-once settlement, remaining
cyber/402/429 combinations and cumulative G0 evidence remain separate work.
Local synthetic results are not proof of production loss or a verified
network-none runtime. These focused fixtures use only the injected upstream
recorder and do not perform provider calls. See the broader-run limitation below.

## Verification status and broad-run block

Focused normal Go test run: all 18 EOF cases and 8 parent scenarios passed
(29 passing events including parent summaries), zero skips. Fixture SHA-256:
`1bb662bf42cd4a8eb4dafa21895f605798a3ff83355ee4684212dd4359f80b80`.
Independent static review accepted the cancellation ordering and narrow scope.

The subsequent **full service race pipeline is BLOCKED, not passed**: automatic
review rejected an unexpected external privacy-settings HTTPS call from retained
`TestTokenRefreshService_RefreshWithRetry_Antigravity`. That test uses synthetic
`ag-token` but does not mock its privacy side effect. The request is a POST to
`daily-cloudcode-pa.googleapis.com/v1internal:setUserSettings` with JSON
`{"user_settings":{}}`; delivery is unknown. The rejected operation was not
retried or bypassed. The session is no longer addressable; queued handler/vet
stages have no completed result and remain NOT_RUN for this candidate.

This also prompted the explicit evidence correction on parent #27. Fixing the
unrelated refresh fixture requires a separate reviewed mock; it is not hidden
behind a test exclusion or claimed full green here. Reviewer local Gitea
acceptance and G0 remain pending. No public workflow change is needed.

## Review and rollback

Reviewers pull the stacked PR and execute local Gitea CI, as requested. Public
GitHub workflow selection is unchanged and is not the acceptance authority.
Record exact parent/head/tree, tool versions, test events, skips and Gitea run;
for broader G0 record actual PG/Redis fixtures and readiness/cleanup separately.

```sh
cd backend
go test -tags=unit -count=1 -timeout=60s -run '^TestPassthrough(EOFFlushForwardResult|FlushOnly)' ./internal/service
# Full service race: BLOCKED pending reviewed external-side-effect isolation.
```

Rollback only this patch to return to #27's normal-boundary fix, retaining that
parent's protection but reintroducing the reproduced EOF-residual gap. No data
rollback is needed. Neither PR authorizes merge or deployment.
