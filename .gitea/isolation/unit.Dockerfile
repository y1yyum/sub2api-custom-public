ARG GO_IMAGE
ARG PREPARED_IMAGE=scratch
FROM ${GO_IMAGE} AS validation-dependencies
USER root
RUN apt-get update && apt-get install -y --no-install-recommends python3 \
    && rm -rf /var/lib/apt/lists/*
ENV GOTOOLCHAIN=local GOMODCACHE=/opt/gomodcache
WORKDIR /prepare
COPY backend/go.mod backend/go.sum ./
RUN test "$(go version | awk '{print $3}')" = go1.27.3 \
    && go mod download && go mod verify \
    && mkdir -p /opt/isolation/compiled \
    && chown 65532:65532 /opt/isolation/compiled \
    && go list -m -json all > /opt/isolation/modules.json \
    && dpkg-query -W > /opt/isolation/os-packages.txt
COPY isolated-unit-entrypoint.py critical-regressions.py probe-isolation-environment.py /opt/isolation/
WORKDIR /
USER 65532:65532

FROM ${PREPARED_IMAGE} AS validation-tests
COPY backend /src/backend
COPY frontend/src/constants/platformCatalog.builtin.json /src/frontend/src/constants/platformCatalog.builtin.json
COPY validation-receipt.json /src/validation-receipt.json
COPY source-manifest.json /opt/isolation/source-manifest.json
COPY compile-isolated-tests.py /opt/isolation/compile-isolated-tests.py
ARG SOURCE_MANIFEST_SHA256
ARG VALIDATION_PHASE=full
ARG COMPILE_INVOCATION
WORKDIR /src/backend
RUN --network=none test -n "$COMPILE_INVOCATION"
RUN --network=none /usr/bin/env -i \
    PATH=/usr/local/go/bin:/usr/bin:/bin HOME=/tmp/home TMPDIR=/tmp \
    GOCACHE=/tmp/build-cache GOTMPDIR=/tmp/build-tmp GOMODCACHE=/opt/gomodcache \
    GOPATH=/tmp/gopath GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local \
    GOMAXPROCS=2 GOGC=30 GOMEMLIMIT=3GiB CI=true \
    /usr/bin/python3 -I /opt/isolation/compile-isolated-tests.py \
    --suite unit --phase "$VALIDATION_PHASE" --source-root /src \
    --manifest /opt/isolation/source-manifest.json --manifest-sha256 "$SOURCE_MANIFEST_SHA256" \
    --output /opt/isolation/compiled \
    && rm -rf /tmp/build-cache /tmp/build-tmp /tmp/home /tmp/gopath
