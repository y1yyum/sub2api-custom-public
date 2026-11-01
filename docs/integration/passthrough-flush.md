# INT-01a — passthrough Flush-only disconnect

Base: `f347674975c1bfae5a38c60eed43fdda164fc1de`.
This is a bounded first INT-01 sub-batch, not completion of G0.

## Reproduction and scope

The ordinary Responses path already uses `httpresponse.Flush` to observe
transport errors through supported wrappers. The passthrough path instead used
void `http.Flusher.Flush`, hiding a FlushError while the request context stayed
live. Buffered `Write` calls can continue succeeding after such a failure.
Consequently the existing bounded disconnect drain was not entered. The
streaming helper's disconnect flag remained false; the Forward error path could
then discard its usage result instead of handing it back for settlement.

The fixture uses Gin's actual response writer over a synthetic FlushError-capable
transport. It successfully flushes initial visible output, fails the second
visible event's flush, and releases bare error/final usage only after that real
flush attempt. Writes deliberately continue succeeding after failure so a later
Write error cannot mask the defect. This matters because connected OpenAI
passthrough suppresses bare `error` events: simply configuring a writer to fail
on that event does not exercise the failure.

The narrow fix reuses `httpresponse.Flush` and enters the existing
`startDisconnectDrain` on failure. It does not alter connected error semantics,
provider retries, billing policy, drain budget, authorization or database schema.

## Acceptance boundary

Cases cover late authoritative failed/completed usage, held-open budget expiry,
a connected control, resource closure and no additional downstream writes after
disconnect. Forward-level coverage checks result handoff and a single upstream
request; it does **not** prove a database row was settled exactly once.

Still required by the broader INT-01/G0 plan: full handler + Ops capture wrapper
and real-repository PostgreSQL exactly-once settlement; asynchronous audit/cyber
cases and remaining 402/429 semantics; independent cumulative Gitea acceptance.
No production-loss claim, real provider call or verified network-none runtime is
implied by these local synthetic fixtures.

## Verification correction — unexpected external fixture side effect

Read-only review on 2026-11-01 found that the **full retained service suite**
ran `TestTokenRefreshService_RefreshWithRetry_Antigravity`, which mocks token
refresh but not the subsequent privacy-settings request. Its source constructs
an HTTPS POST to `daily-cloudcode-pa.googleapis.com/v1internal:setUserSettings`
with JSON `{"user_settings":{}}` and the literal synthetic bearer `ag-token`.
The recorded #27 run reports `context deadline exceeded`; delivery is unknown,
and no successful settings change was established. It is not a model inference
request and no real credential or customer prompt was found in that fixture.

The numerical 84-test / 178-event PASS below remains the actual process result,
but **is not local-only or hermetic acceptance evidence**. The focused new
passthrough fixtures and selected handler fixtures use recorded/mock upstreams;
this external side effect belongs to the unrelated retained refresh test.
A later cumulative run was blocked by auto-review after the same external path
was detected. It was not retried. Do not rerun the full suite unisolated: first
review an explicitly mocked privacy seam or have the reviewer enforce approved
network isolation. No public CI workflow change is requested or included.

## Local evidence

With Go 1.27.3, the final fixture against the unchanged base production source
had six disconnected scenarios fail (eight failed test events including parent
summaries), while both connected controls passed. Forward returned nil in the
disconnected error cases. After the narrow fix, all eight scenarios passed
(ten passing events including parent summaries), with zero skips.

The full retained service package also passed with `-race -tags=unit -count=1`:
84 top-level tests, 178 passing test events, zero failures/skips. `go vet` for
the service package passed. These counts include the new fixture and are not
the historical DB contract count.

Existing ordinary-Responses handler regressions also passed with race enabled:
`TestCustomLateBareErrorDisconnectSettlesFinalUsageExactlyOnce`,
`TestCustomConnectedBareErrorRemainsTerminal`, and
`TestCustomLateDisconnectDrainControls` (3 top-level / 13 passing events, no
skips). Those handler fixtures use mocked usage repositories, not PostgreSQL.

Fixture SHA-256:
`fc6f4648199d05c62fdeb32e0494b3335c96542bab4a6733f4ffda03c50e0f4e`.
The existing deferred EOF-residual flush and the rest of the INT-01 matrix are
not claimed covered by these normal SSE-boundary scenarios.

## Reviewer handoff

Pull this Draft PR locally and run the reviewer-owned Gitea CI. Do not change
public-repository CI workflows or replace full contract acceptance with public
scoped green status. Record exact head/tree, actual test events, skips, tool and
DB versions, readiness/cleanup, and required blocked contracts separately.

Focused commands (Go 1.27.3):

```sh
cd backend
go test -tags=unit -count=1 -timeout=60s -run '^TestPassthroughFlushOnly' ./internal/service
go test -race -tags=unit -count=1 -timeout=60s -run '^TestPassthroughFlushOnly' ./internal/service
```

Rollback: revert this narrow patch; no migration or data rollback. A revert
reintroduces the reproduced Flush-only disconnect classification gap.
