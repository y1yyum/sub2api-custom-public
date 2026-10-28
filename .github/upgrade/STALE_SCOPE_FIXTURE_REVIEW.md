# Stale-scope snapshot fixture initialization

PR2 run40686168891 integration race report identifies the test's assignment to
`runtimeCacheTTL` after NewContentModerationService launched polling workers,
concurrent with worker runtimeSnapshotTTL reads. This is a test setup race, not
an established production configuration update contract or a reason to add Close.

Only TestRecordCyberPolicyEvent_RuntimeSnapshotRefreshFailureKeepsStaleScope is
adapted. Initialize settingRepo, the same banCountArgsTestRepo, and time.Minute TTL
in a service literal before any snapshot operation. Existing runtimeCacheTestService
in content_moderation_runtime_cache_test.go uses this snapshot-only fixture pattern.
The fixture does not exercise background queue processing or retention cleanup.

RecordCyberPolicyEvent still invokes the real loadRuntimeSnapshot and real
triggerRuntimeSnapshotRefresh goroutine. It uses the provided setting/log repos,
TTL and zero-value atomics/mutex; the HTTP client, queue, workerCount and key-health
map are unrelated to this tested path. No elapsed wait/count/assertion is changed:
expired snapshot, failed refresh, stale-scope log and exactly two settings calls
are all still required. The minute TTL is retained, not replaced by default1s.

Both complete B/U test files and the exact function are pinned. Reverse replacement
must reproduce the entire original test file. In addition, adaptation verifies the
entire reviewed production content_moderation.go hash: future constructor defaults,
fields or call-path changes fail closed for a new fixture review. Production source
is never altered. No worker shutdown API, skip, retry or new test partition is added.
