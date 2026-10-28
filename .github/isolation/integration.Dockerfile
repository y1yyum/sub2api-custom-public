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
    && apt-get install -y --no-install-recommends python3 gcc g++ git libatomic1 \
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
COPY validation-receipt.json /src/validation-receipt.json
COPY source-manifest.json /opt/isolation/source-manifest.json
COPY isolated-unit-entrypoint.py /opt/isolation/isolated-unit-entrypoint.py
COPY integration-bootstrap.py /opt/isolation/integration-bootstrap.py
WORKDIR /src/backend
USER 65532:65532
# No inherited entrypoint, command, environment, ports, healthcheck or VOLUME.
# The host provides the reviewed fixed env -i / Python invocation explicitly.
