# Cumulative validation only — not an additional production change set

The original composition below is historical. The current review-repair
composition and its additional startup contract are recorded in
`kiss-cumulative-review-repairs.md`; its mandatory total is 32, not 31.

This temporary Draft PR combines the exact reviewed heads below to execute full
interaction CI. Do not merge it in addition to PRs #7–#13: that would duplicate
the review/integration path. No PR merge, release or deployment is performed.

Baseline: `d5eae9e2a26c94c285f3fdbbb5ad581d3b85301e`.

| PR | Exact source head |
|---|---|
| #7 | `7a4668355ab1c86f7d4ab7e9cf6fd57596036010` |
| #8 | `ada0637b2e0cbdb474cc0049b6bf2518da7a01c6` |
| #9 | `320f4d9de70a77dabc6790a9eb17c9f2f729e992` |
| #10 | `37f62de4ff8112d41865aa6d3cd33189a5eeadcc` |
| #11 | `2b9bd6a759341323ae01651d3ef30e3e06022f37` |
| #12 | `f13173c171cc5430b8915eca456867064d25dc3f` |
| #13 | `6569337d300cd711f6e21112003decbc96d53bed` |

Merge order: #7, #8, #9, #12, #10, #11, #13. #12 already contains the
actual #9 head; true parent ancestry is retained. Remote source heads were read
and fetched immediately before constructing this branch.

Only known harness conflicts were resolved: preserve both parser and probe
adaptations, and union batching/migration mandatory tests with the existing 29
contracts (31 total). All SQL filenames and bytes remain unchanged.

The owned parser-plus-gzip admission interaction test is retained from local
reviewed composition `6db89adf0ceb53f58b3b1343d7c1a9f07f1790ed`.
Apart from this document, the resulting tree is byte-identical to that reviewed
composition; the difference is ancestry now binds actual published PR heads.

Static/restoration proof is not runtime validation. Full combined tests must run
through the existing reviewed offline-compile and network-none unit/integration
containers. No host Go execution, live services, credentials, socket mounts or
network-policy relaxation is introduced. CI receipts must establish all 31
mandatory database contracts and exact package/source/partition coverage.
