# INT-02a — explicit privacy-side-effect test isolation

Independent branch from `f347674975c1bfae5a38c60eed43fdda164fc1de`.
This is a test dependency/fixture correction, not a provider behavior change.

## Why this is separate

The retained `TestTokenRefreshService_RefreshWithRetry_Antigravity` mocked token
refresh but allowed its post-refresh privacy hook to create a real HTTP client.
That hook attempted a settings POST using literal test bearer `ag-token` and
`{"user_settings":{}}`. A later full-service run was blocked by automatic review;
that denied command was not retried or bypassed. The earlier numerical PASS did
not establish local-only execution, and PR #27 records the correction.

## Explicit isolation, not test exclusion

- Add one unexported, per-instance privacy-setter dependency. Nil still selects
  the existing production implementation; no configuration/wire change, global
  transport patch, default endpoint change, or production privacy bypass.
- The existing refresh test supplies a local recorder before execution. It
  asserts one invocation with the newly refreshed synthetic token and expected
  empty project/proxy, then verifies persisted failure mode and retains the
  original credential-update/cache-invalidation assertions.
- A separate Antigravity client test constructs `Client` with an explicit
  in-memory `http.RoundTripper`, never `NewClient` or a default transport. It
  verifies POST URL/Host, headers and exact JSON; synthetic success, HTTP error
  and malformed JSON responses verify parsing and body closure. The transport
  never delegates, resolves DNS, opens a socket, or follows an external redirect.

Only the two named tests below are candidates for local execution after
independent source review establishes those paths. They are not a full-suite
retry or a substitute for G0. No assertion is removed, no test is skipped, and
no public CI workflow or security exception is changed.

```sh
cd backend
go test -race -tags=unit -count=1 -run '^TestTokenRefreshService_RefreshWithRetry_Antigravity$' ./internal/service
go test -race -tags=unit -count=1 -run '^TestClientSetUserSettingsOwnedTransport$' ./internal/pkg/antigravity
```

The previous broad service/handler/vet pipeline remains BLOCKED/NOT_RUN for the
EOF candidate as documented in PR #28. Do not infer that this single correction
proves every retained test is hermetic or permits replaying the denied command.
Reviewers pull the Draft PR locally and run approved local Gitea acceptance.

Rollback: revert the dependency seam and explicit mocks together. This restores
the old test's unintended external path, so do not run that reverted test
without separately approved isolation. Production nil-default behavior is
unchanged in either version. No schema rollback is involved.

## Actual focused verification

After independent static review of both exact paths, the normal tool approved
execution. Go 1.27.3 with `-race -count=1` and the anchored expressions above:
refresh regression **PASS** (1 test event); owned transport **PASS** (3 response
cases + parent, 4 events); zero failures/skips. `GOTOOLCHAIN=local`,
`GOPROXY=off`, and `GOSUMDB=off` were set; no network fallback was used.
This records focused mock execution, not a network-namespace certification or
full-suite acceptance. The earlier denied command was never rerun.

`gofmt` and `git diff --check` pass. No additional full suite or full candidate
vet result is claimed for this branch.
