# Selective KISS P0: one top-level model contract

Baseline custom main: `d5eae9e2a26c94c285f3fdbbb5ad581d3b85301e`.
Reviewed KISS snapshot: `d2d3773e80fb2f8dfdae58d0c4c77235764c3d76`.
Source references: `f1e705db315a563b4c6597e8b8563d1fe6dafa52` and
`04fd888ccddca31cbdda9ed29026f7d19a933e06`. This is a selective redesign,
not a merge of the fork or its unrelated payload/media/crypto changes.

## Deliberate compatibility decision

Reject **every repeated top-level JSON `model` key**, including identical values,
case variants and JSON-escaped keys. Count keys regardless of whether their values
are strings, empty, null, numbers or objects. Nested `model` fields remain legal.
A single key keeps its existing protocol parser's type/case rules. Malformed JSON
remains a parser error, not a successful fallback in Responses/Chat parsing.

The source fork has conflicting policies: one service rejects all duplicates,
while its later handler predicate only rejects conflicting nonempty string
values. Combining those implementations would leave route-dependent behavior and
miss wrong-type/empty duplicate values. This port uses one shared decoded-key
predicate and one error identity instead. Clients sending identical duplicate
keys must send the field once. This tightening is intentional, not test flakiness.

HTTP admission runs after authentication, before allowlist checks, composite
rewrites, account selection and billing on all six gateway chains. It operates
independently of whether an allowlist is configured. The same lenient-normalized
view accepted by text handlers is checked, preventing BOM/raw-control repair from
hiding duplicates; original body bytes are restored unchanged for the actual
protocol parser. Existing encoded/body/normalized size limits still apply.
Multipart form fields and nested `session.model` semantics are not redesigned.

WebSocket first frames and subsequent frames use the same policy. Native parsing
and passthrough frame ingress additionally check the original bytes before
compatibility/model mutations can collapse duplicate fields. Rejections close
with policy violation1008 and never send the rejected request upstream.

Service parsing, cloning and replacement propagate the shared error. Chat and
Responses session-hash parsing no longer discard parse errors and fabricate a
fallback parsed request. Gemini is explicitly URL-model-driven: duplicate errors
are rejected, while unrelated optional session-hash errors retain the previous
nil-hash fallback. No legal single body model is made the Gemini routing source.

## Tests and retained guarantees

Owned tests cover identical/different/case/escaped/wrong-type duplicate keys,
nested/single valid models, malformed JSON, parser clone/replacement, HTTP
admission before downstream side effects (including lenient inputs), read limits,
and both WebSocket ingress modes with allowlists disabled.

The pinned .19 WS test that intentionally allowed identical repeats is adapted to
require rejection in both modes. Three existing duplicate-rejection cases now
assert the precise duplicate error rather than an allowlist error. Status1008,
no-upstream assertions and every unrelated test body remain intact. A pure Python
reverse-transform check proves that narrow change and the six-chain wiring.
Standalone allowlist candidate enumeration remains unchanged: it still inspects
all possible parser values, but no longer serves as the duplicate-policy gate.

All execution remains through the existing network-none CI runner, with exact
source/binary receipts, exhaustive service registry coverage and all29 mandatory
DB contracts. No migration, credential, runtime OAuth, scoped-admin, refund247,
image billing, UI or moderation behavior is replaced. No live account request,
merge, deployment or release is part of this PR.
