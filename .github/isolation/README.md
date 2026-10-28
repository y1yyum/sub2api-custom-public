# Offline unit-test execution boundary

This changes the previously quarantined operation: **application/test code only
executes in a Docker container with no external network interface**. It does not
authorize real cloud requests and does not claim the old unrestricted suite is safe.

## Phases

1. On the runner, copy the exact clean tracked checkout into the pinned validation
   overlay. Only receipted backend files and its validation receipt enter a new
   allowlisted build context. Reject symlinks, extra backend files, `.git` and
   `.env` files. There are no user directories, credentials, SSH agents or host
   sockets in that context.
2. Resolve Go 1.27.3's official image to a digest. The networked build only installs
   Python from signed Debian packages and downloads/verifies the Go dependency
   graph. It never invokes a test, test binary, `go test -run`, package init,
   TestMain, or source-provided command. Source is copied, not executed.
3. Create (not start) an immutable image-ID container with `--network none`,
   UID/GID 65532, all capabilities dropped, no-new-privileges, a read-only root,
   private IPC/cgroup namespace, no host binds/devices/ports and bounded resources.
   Only a new `/tmp` tmpfs is writable. Inspect these settings and the exact
   executable/arguments before starting it; any mismatch fails closed.
4. The runtime preflight independently checks loopback-only interfaces/routes,
   identity, capabilities, mounts, offline environment and every source digest.
   Only then run the full `unit`-tag suite with race detection and no test filter.
   No fallback enables networking or runs the suite on the runner host.
5. Preserve exact source, image/base digest, dependencies, host/runtime checks and
   actual test JSON/exit status as CI artifacts. A timeout, missing receipt, OOM,
   assertion failure or isolation failure remains a failure.

Docker's documented `none` driver creates only loopback, not an external bridge:
https://docs.docker.com/engine/network/drivers/none/ . This constrains custom HTTP
transports, proxy code and direct sockets as well as default HTTP clients. No
IPv6 loopback is promised by Docker; fixture incompatibilities must be diagnosed,
not "fixed" by reattaching a network.

## Reproduce

Docker must already be available in an authorized environment. Commit the exact
source first. From the repository root:

```sh
python3 -m unittest discover -s .github/isolation -p 'test_*.py' -v
python3 .github/scripts/run-isolated-unit.py /tmp/sub2api-isolated-unit-new
```

The output directory must be new and outside the checkout. No Docker socket is
mounted into the test container. The host Docker CLI performs creation/inspection
and copies receipts after the test; test code cannot control the daemon.

## Isolated integration follow-up

The separate integration follow-up removes test access to Docker entirely. It
uses the same host/runtime preflights with an official PostgreSQL 18.1 / Redis
8.4 dependency image, fresh loopback databases and per-test private Redis Unix
sockets. The prior unrestricted integration command is not an execution path.
See [integration runtime](INTEGRATION_RUNTIME.md) and
[fixture adaptations](../upgrade/INTEGRATION_ISOLATION.md).

```sh
python3 .github/scripts/run-isolated-unit.py --suite integration /tmp/sub2api-isolated-integration-new
```

Both lanes remain fail-closed on isolation/fixture failure. Source assertions are
not skipped to obtain green. Prompt-audit's six PostgreSQL tests and three Redis-dependent tests are explicitly
classified as integration instead of silently skipping the unit lane without a
DSN. Inherited upstream skips are individually recorded in artifacts; they are
not passing coverage. Actual CI execution/receipts are required before claiming
either application suite passes. No merge, release, deployment or live database
operation is authorized by this test harness.

## Offline compile-only images (storage correction)

The first isolated run proved the runtime boundary, but a cold race build filled
its 4 GiB private tmpfs: two packages never linked. Increasing writable host
access or disabling race/readonly checks is not the fix. Dependencies are still
prepared separately; the exact receipted source is then compiled with
`go test -c` in an explicit Docker build `RUN --network=none` step. This step
never executes a test binary, package init, or TestMain. Package/test-file and
binary-hash inventory is preserved in `compile-receipt.json`. Runtime verifies
that inventory and runs every binary, in its original package working directory,
through `test2json`, with the same tags, race instrumentation, count and timeout.
There is no fallback to networked or runtime suite compilation. Tests that
explicitly exercise code generation (the Ent schema fixture) retain that behavior
inside the verified offline runtime and use private temporary scratch.

`image-build.log` and `build-capacity.json` preserve compile failures and actual
Docker-storage free space / runner memory observations. Build failure is a hard
failure before tests, never an empty passing suite. The existing runtime limits,
read-only source/root, private tmpfs, nonroot identity and no-mount/no-socket
boundary remain unchanged. These corrections require a new actual CI run; static
checks alone do not establish complete backend coverage.

The second compiled unit run completed all 107 packages (56 test binaries),
including the previously unlinked service/setup packages. Its remaining failures
exposed unsynchronized restored test stubs and process-global test interference;
these are not evidence of a deployed service failure. Pinned fixture adapters
synchronize HTTP capture/counters/cache snapshots and wait for the real quota
worker completion marker before clearing test state. No assertion, race check,
allocation threshold or test is removed. The platform catalog synchronization
fixture now includes exactly its hash-pinned public JSON input, not the frontend
source tree generally. DNS-independent field-validation cases use a documentation
IP literal on the existing no-dial validation path; private-IP rejection remains
tested. Standard CA certificates are installed only for verified dependency
preparation, with TLS validation retained.
