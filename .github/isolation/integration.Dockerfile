# Host must resolve these exact official tags to sha256 digests and verify them:
# postgres:18.1-bookworm, redis:8.4-bookworm, golang:1.27.3-bookworm.
# No source/test code is executed during this networked dependency-only build.
ARG PG_IMAGE
ARG REDIS_IMAGE
ARG GO_IMAGE
FROM ${GO_IMAGE} AS go_toolchain
FROM ${REDIS_IMAGE} AS redis_binary
FROM ${PG_IMAGE} AS fixture_dependencies
USER root
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates python3 gcc g++ git libatomic1 \
    && rm -rf /var/lib/apt/lists/* \
    && test -z "$(getent passwd 65532)" \
    && test -z "$(getent group 65532)" \
    && groupadd --gid 65532 fixture \
    && useradd --uid 65532 --gid 65532 --no-create-home --home-dir /tmp/home --shell /usr/sbin/nologin fixture
COPY --from=go_toolchain /usr/local/go /usr/local/go
COPY --from=redis_binary /usr/local/bin/redis-server /usr/local/bin/redis-server
ENV PATH=/usr/local/go/bin:/usr/bin:/bin GOTOOLCHAIN=local GOMODCACHE=/opt/gomodcache
WORKDIR /prepare
COPY backend/go.mod backend/go.sum ./
RUN test "$(go version | awk '{print $3}')" = go1.27.3 \
    && go mod download && go mod verify \
    && mkdir -p /opt/isolation \
    && go list -m -json all > /opt/isolation/modules.json \
    && dpkg-query -W > /opt/isolation/os-packages.txt

# The official postgres image declares a VOLUME. A scratch final stage copies
# filesystem bytes, not image metadata, so Docker cannot silently attach an
# anonymous data volume. Host inspect must still require Config.Volumes empty.
FROM scratch
COPY --from=fixture_dependencies / /
# Source is copied only after all network-enabled build steps have finished.
COPY backend /src/backend
# Exact public catalog fixture only; no broader frontend tree or credentials.
COPY frontend/src/constants/platformCatalog.builtin.json /src/frontend/src/constants/platformCatalog.builtin.json
COPY validation-receipt.json /src/validation-receipt.json
COPY source-manifest.json /opt/isolation/source-manifest.json
COPY isolated-unit-entrypoint.py /opt/isolation/isolated-unit-entrypoint.py
COPY integration-bootstrap.py /opt/isolation/integration-bootstrap.py
COPY compile-isolated-tests.py /opt/isolation/compile-isolated-tests.py
ARG SOURCE_MANIFEST_SHA256
RUN mkdir -p /opt/isolation/compiled && chown 65532:65532 /opt/isolation/compiled
WORKDIR /src/backend
USER 65532:65532
# Compilation only: go list and go test -c never execute a test binary, init or TestMain.
# Missing dependencies fail offline; no retry with networking and no runtime compilation fallback.
RUN --network=none /usr/bin/env -i \
    PATH=/usr/local/go/bin:/usr/bin:/bin HOME=/tmp/home TMPDIR=/tmp \
    GOCACHE=/tmp/build-cache GOTMPDIR=/tmp/build-tmp GOMODCACHE=/opt/gomodcache \
    GOPATH=/tmp/gopath GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local \
    GOMAXPROCS=2 GOGC=30 GOMEMLIMIT=3GiB CI=true \
    /usr/bin/python3 -I /opt/isolation/compile-isolated-tests.py \
    --suite integration --source-root /src --manifest /opt/isolation/source-manifest.json \
    --manifest-sha256 "$SOURCE_MANIFEST_SHA256" --output /opt/isolation/compiled \
    && rm -rf /tmp/build-cache /tmp/build-tmp /tmp/home /tmp/gopath
# No inherited entrypoint, command, environment, ports, healthcheck or VOLUME.
# The host provides the reviewed fixed env -i / Python invocation explicitly.
