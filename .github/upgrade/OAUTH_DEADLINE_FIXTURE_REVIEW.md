# Late-success deadline fixture ordering

The actual isolated PR8 green unit artifact reports
`TestRefreshIfNeeded_LateSuccessAfterDeadlineDoesNotPersist` failing at restored
`oauth_refresh_api_test.go:650`: expected `context.DeadlineExceeded`, received nil.
The same event sequence records `oauth_refresh_success_cas_skipped_stale_credentials`.
The test lasted about 40 ms. This establishes the observed failed expectation,
not a claim that the intended deadline boundary was already signaled.

The pinned `.19` fixture independently schedules a 10 ms context timeout and a
30 ms mock sleep. Wall-clock delay does not establish that context cancellation
has been processed before the mock returns. Production already checks `ctx.Err()`
immediately after executor.Refresh; no production OAuth change is proposed.

Only the named test changes: remove its 30 ms delay and set its existing
`executor.onRefresh` hook to wait on `ctx.Done()` after context creation. The
shared stub remains byte-identical.
The mock deliberately still returns successful credentials after Done closes,
so the API—not the mock—must reject the late result. Existing mock delay, callback,
error and credentials behavior remain unchanged for every other test. The target
keeps its 10 ms deadline and all three assertions: DeadlineExceeded, nil result,
and zero credential persistence. There is no larger sleep, cancellation-error
shortcut, added goroutine, skip, race-disable, or production lifecycle/API edit.

A static test reverses the two exact target-test replacements and compares the
entire original source byte-for-byte. It separately checks unchanged test and
assertion counts and the exact target test body except the removed delay and added hook. The input `.19` SHA-256 is pinned; ambiguous anchors fail preparation.
Existing mandatory regression inventories are untouched. No host Go/backend or
Docker command was executed; runtime success requires reviewed isolated CI.
