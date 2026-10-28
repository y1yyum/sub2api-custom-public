# Fixed service test-process partitions

This is a test-harness arrangement, not a production `Close` change or proof of
an application lifecycle defect. No allocation assertion is changed: the
existing strict `require.Less(t, largeAllocs, 200.0)` check remains intact. A failure in the fresh
process remains a genuine failing result.

The schedule is fixed in source, before observing any test result:

- `unit`: one main service process, one process for
  `TestGroupUsageDateUsesConfiguredTimezoneBoundary`, and one process for
  `TestSanitizeOpenAIResponsesToolParameterTypes_RewriteCountIndependentOfHits`.
- `integration`: one main service process and one process for the same allocation
  test. The timezone test is unit-tagged and must be absent from this registry.
- Other packages retain their original single-process execution.

Inside the verified offline runtime, the **same verified race binary** is first
invoked with `-test.list=.`. This metadata process can run `init`/`TestMain`, so it
is never invoked in networked dependency preparation. Missing/renamed fixed
names, duplicate names, unexpected registry stdout, listing failure, or an
overlong regex fail closed. Tests, examples and fuzz seed functions are included;
benchmarks are recorded but not executed, matching the original default command.

A prefix-trie regex covers precisely every remaining top-level name. Each
partition's anchored regex is audited against the full registry: their union
must be exhaustive and pairwise disjoint. Main is never split into ad-hoc chunks.
All matching subtests and fuzz seeds retain default execution. All processes use
the same binary hash, environment, package cwd, `-test.count=1`, and 20-minute
per-process timeout. All scheduled processes run once; failures never trigger
retries or change the schedule. Receipts include the registry, regexes, commands,
per-partition exits, and binary hash.

Only service children (including the listing process) receive the explicitly
synthetic public `GEMINI_CLI_OAUTH_CLIENT_ID=custom-test-client` before package
initialization. The existing OAuth test supplies its fake secret itself. No
secret, host variable, production OAuth default, or other package environment is
changed. This mirrors the production startup-snapshot contract rather than
changing production initialization to satisfy a late test `Setenv` call.
