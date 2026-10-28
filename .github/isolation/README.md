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

## Integration remains blocked

The existing integration fixtures invoke Docker/Testcontainers and access mapped
PostgreSQL/Redis endpoints. They cannot run unchanged in this boundary. Their CI
job continues to fail explicitly before execution; no skipped-green replacement.
A separate reviewed local PostgreSQL/Redis fixture implementation is needed.
Existing inherited skips are reported in test JSON, not erased or counted as
passed. This work does not merge, publish an application image, deploy or migrate
a live database. Actual unit success must be established by the new CI run; design
and Python validator tests alone do not prove application correctness.
