# Account-probe destination policy

This selective port redesigns the boundary rather than copying KISS commit
`639264d4807a44a90da0e2f926c8dc98ead491df`. DNS prechecks alone do not bind a
subsequent dial, DNS failures must not allow a request, and production globals
must not provide test bypasses.

## Explicit private-service compatibility choice

`security.account_probe.allow_private_hosts` defaults to **false**. It only
controls account connectivity tests, their Grok realtime WebSocket handshake,
and the billing probe's account-test transport. It does not change ordinary
gateway forwarding or `security.url_allowlist.allow_private_hosts` defaults.

Operators who intentionally probe an RFC1918/ULA or loopback model service can
explicitly set this new option to true. This does not authorize metadata or
link-local endpoints, unspecified/multicast addresses, shared/benchmark/reserved
ranges, or IPv6 translation/tunneling ranges. Existing enabled host allowlists
still apply. Existing HTTP-vs-HTTPS policy is unchanged (enabled host allowlists
continue to require HTTPS).

No deployment configuration is changed by this PR, and no live probe, account
credential or database is used to test it.

## Transport invariants

* Policy is attached by the server's account-test constructor, not a client
  request field. Existing persisted account proxy selection remains authoritative.
* Resolve each destination through an instance-local resolver; empty/error/mixed
  prohibited answers fail closed. Normalize IPv4-mapped addresses. Validate every
  address before dialing any. The dial receives only the validated literal IP,
  while HTTP Host and TLS SNI retain the original destination hostname.
* No environment proxy and no shared upstream connection cache. Each hop gets a
  separately validated transport. API credential headers never reach a rejected
  destination; cross-origin redirects are rejected, same-origin redirects resolve
  and validate again. Existing no-redirect/public-only markers remain stricter.
* A persisted administrator-selected proxy is a separate trusted network hop.
  HTTP/HTTPS proxies receive CONNECT to a validated literal (also for HTTP
  destinations); SOCKS receives a literal address, never delegated hostname DNS.
  Proxies that do not support CONNECT to the requested target port fail closed;
  there is no fallback to remote DNS or a direct route. The proxy endpoint itself
  may be private under the existing configured-proxy trust contract.
* Proxy connection/handshake and TLS-fingerprint handshakes have finite 10-second
  bounds and respect cancellation. No `InsecureSkipVerify` or global test escape.
* Unmarked gateway traffic retains its existing transport behavior. This PR is
  not a claim that every other outbound subsystem has been audited for SSRF.
  Provider token-refresh clients and delegated Antigravity connectivity code use
  their existing provider-owned transports, not this constructor decorator;
  those fixed-provider paths are outside this selective account-base-URL boundary.

## Tests and limitations

Owned unit tests use synthetic DNS answers and `net.Pipe` to inspect literal
TCP/SOCKS/CONNECT targets, Host and ClientHello SNI, cancellation, redirects and
credential non-transmission. The repository integration seam uses a disposable
loopback httptest server under the reviewed network-none test container. No
external service is contacted. Actual CI results must be recorded in the Draft
PR before claiming runtime validation; source/static review alone is insufficient.

The pinned upstream billing cadence/concurrency fixture uses a mocked HTTPUpstream
and loopback URL. Its sole adaptation explicitly sets the new private-probe option
in that fixture's configuration; all payloads/assertions/test bodies are unchanged.
New owned tests separately assert the default-deny behavior and blocked metadata
even under private opt-in. No production-global escape is introduced.
