# Selective KISS: distinct-start account window statistics

Custom baseline: `d5eae9e2a26c94c285f3fdbbb5ad581d3b85301e`.
Source: KISS `d1d2f5c25110961b5726b35b248c92b9a0ec88db`, reviewed within
`d2d3773e80fb2f8dfdae58d0c4c77235764c3d76`. No unrelated fork changes.

The admin account list previously ran one window-statistics SQL aggregate per
eligible Anthropic OAuth/setup-token account. It now captures each unique
account's own effective start once and performs one parameterized VALUES join.
There is deliberately **no shared minimum start**: accounts with newer windows
must not include an older account's interval. Existing inclusive created_at>=start
semantics, token expressions, account-statistics override/multiplier, standard
cost and user cost are unchanged. The UI still receives StandardCost, not the
multiplied account cost.

- Sort IDs for deterministic parameter pairing; bind each ID as bigint and its
  own time.Time as timestamptz. Values are never interpolated into SQL.
- Maximum1000 accounts matches existing admin pagination, bounding SQL to2000
  binds. Oversized internal requests fail explicitly before repository access.
- Empty input performs no query; requested accounts without rows receive zeros.
- Duplicate IDs are captured once by the handler; first occurrence supplies the
  same account's window. No duplicate VALUES row can multiply aggregate totals.
- Query/scan/row errors propagate. Admin decoration remains best effort; failed
  batch requests do not trigger a hidden per-account retry/fan-out.
- A narrow optional repository capability preserves compatibility with existing
  test stubs/alternate implementations. Its legacy fallback preserves per-account
  starts and partial-success behavior; cancellation stops further fallback work.
- No cache, cost policy, window duration/timezone calculation, migration, account
  probe, credential, OAuth, scoped-admin, refund or UI contract is redesigned.

Owned SQL-mock tests check one query, sorted exact arguments, no-row zeros,
empty/bounded inputs and query/scan/row failures. Service and handler tests cover
batch dispatch, no fallback on batch error, legacy partial results, cancellation,
account eligibility/deduplication, distinct starts and StandardCost projection.

`TestAccountWindowStatsDistinctStartsPostgres` uses only the existing isolated
PostgreSQL fixture. A rollback-only temporary table compares the new real query
with the unchanged per-account query across different starts, exact boundaries,
NULL/default multiplier semantics, missing rows and equivalent timezone instants.
It is added to the fail-closed mandatory integration inventory: prior29 contracts
remain required, total30. No operator DSN or external service is accepted.

Local preparation/static checks are not runtime evidence. Actual Go execution
must remain in the reviewed network-none CI runner, with complete source/binary
receipts and exhaustive coverage. No publication, merge or deployment is implied
by this local candidate.
