# Large-frame acceptance fixture watchdogs

Only `TestOpenAIWSHTTPBridgeAcceptsFirstFrameAboveLegacy16MiB` is adapted.
The complete function is byte-identical at pinned B/U and independently SHA256
locked, in addition to the manifest's separate whole-file input hashes.

Baseline integration run40685514118 failed its local client reader after10.76s
with `context deadline exceeded`; ingress accepted17,825,875 bytes. Earlier
isolated target runs passed this same fixture in7.54s,7.72s and10.18s. This is
wall-clock-margin evidence, not proof of a production bug or the former failure's
root cause. The original failure remains recorded and exact-stage CI must rerun.

This tests acceptance above the legacy16MiB boundary and the HTTP-bridge payload,
complete event sequence, forwarding and result semantics. It has no latency/SLA
assertion. Under the unchanged race instrumentation/two-CPU container, finite
fixture hang watchdogs become: server first-read30s, client write30s, each client
read30s, proxy90s (outlives the local I/O budgets), post-close completion30s.
Dial remains5s. No production timeout or resource/network boundary changes.

The adapter changes exactly five timeout literals in this one function; reverse
normalization must reproduce its original bytes, and all surrounding bytes and
assertions remain identical. There is no retry, skip, reduced payload, threshold
change, result weakening or new process partition. Hash/anchor drift fails closed.
Both B and U source variants are checked statically; each stage keeps its original
test revision/overrides and requires fresh actual isolated outcomes.
