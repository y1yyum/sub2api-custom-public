# Fixed service test-process partitions

This is a test-harness arrangement, not a production `Close` change or proof of
an application lifecycle defect. No allocation assertion is changed: the
existing strict `require.Less(t, largeAllocs, 200.0)` check remains intact. A failure in the fresh
process remains a genuine failing result.

The schedule is fixed in source, before observing any test result:

- `unit`: one main service process, one process for
  `TestGroupUsageDateUsesConfiguredTimezoneBoundary`, one for
  `TestGroupUsageParseDateUsesConfiguredTimezone`, one for
  `TestGroupUsageYesterdayStartHandlesDST`, and one process for
  `TestSanitizeOpenAIResponsesToolParameterTypes_RewriteCountIndependentOfHits`.
- `integration`: one main service process and one process for the same allocation
  test. All three timezone tests are unit-tagged and must be absent from this registry.
- Other packages retain their original single-process execution.

Inside the verified offline runtime, the **same verified race binary** is first
invoked with `-test.list=.`. This metadata process can run `init`/`TestMain`, so it
is never invoked in networked dependency preparation. Missing/renamed fixed
names, duplicate names, unexpected registry stdout, listing failure, or an
overlong regex fail closed. Tests, examples and fuzz seed functions are included;
benchmarks are recorded but not executed, matching the original default command.

A compact prefix-trie complement excludes only the fixed one/four names, using
RE2-compatible alternation and negated character classes (no lookaround). Its
size depends on those fixed names, not the thousands of discovered tests. Proper
prefixes, suffixes and near-neighbor names remain eligible; only exact fixed
names are excluded. The existing argument-size bound is unchanged. Each
partition's anchored regex is audited against the full registry: their union
must be exhaustive and pairwise disjoint. Main is never split into ad-hoc chunks.
All matching subtests and fuzz seeds retain default execution. All processes use
the same binary hash, environment, package cwd, `-test.count=1`, and 20-minute
per-process timeout. All scheduled processes run once; failures never trigger
retries or change the schedule. Receipts include the registry, regexes, commands,
per-partition exits, and binary hash. The host also requires actual top-level
`run` events to equal the discovered tests/examples/fuzz registry exactly once;
missing or unexpected runs and missing/duplicate terminal partitions fail closed.
Explicit test skips are reported, not removed from the coverage obligation.

Only service children (including the listing process) receive the explicitly
synthetic public `GEMINI_CLI_OAUTH_CLIENT_ID=custom-test-client` before package
initialization. The existing OAuth test supplies its fake secret itself. No
secret, host variable, production OAuth default, or other package environment is
changed. This mirrors the production startup-snapshot contract rather than
changing production initialization to satisfy a late test `Setenv` call.


## Resource evidence and bounded cache lifecycle

The prior abrupt main-service exit does **not** establish OOM. Host container
`OOMKilled=false` does not resolve a child-process memory kill. The runner retains
its original `go tool test2json` invocation: its exit is reported as such, not as
a proven underlying test-binary signal. Cgroup-v2 memory events/current/peak/max
are recorded before and after every metadata/test child and after cache cleanup.
Missing counters are unknown, not zero. Cgroup events cover the whole container
(including integration fixtures). RUSAGE_CHILDREN values cover cumulative reaped
children; additive deltas are provided, while maxrss remains a cumulative peak.

After each completed child, only owned directory roots `/tmp/gocache` and
`/tmp/gotmp` are reset to empty mode0700. Root symlinks/unowned roots are rejected;
fd-based symlink-safe removal never follows nested links. Sizes and cleanup
status are recorded. This prevents compile/codegen cache files retained in the
private tmpfs from accumulating across packages; it is not evidence that those
files caused the earlier failure. No change to the 6GiB memory cap, 4GiB tmpfs,
network isolation, read-only source/module cache, PGDATA, or Redis sockets.

Source audit found exactly three callers of the timezone-changing helper in
`custom_group_usage_rollup_test.go`, now all fixed upfront singleton partitions.
The only other service-test timezone initializer is `group_peak_rate_test.go`'s
package `init` setting UTC before tests. This is a complete observed writer set,
not arbitrary suite chunking or a production lifecycle change.
