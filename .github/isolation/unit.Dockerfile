# Runtime source is never executed during the networked dependency preparation.
ARG GO_IMAGE
FROM ${GO_IMAGE}
USER root
RUN apt-get update && apt-get install -y --no-install-recommends python3 \
    && rm -rf /var/lib/apt/lists/*
ENV GOTOOLCHAIN=local GOMODCACHE=/opt/gomodcache
WORKDIR /prepare
COPY backend/go.mod backend/go.sum ./
RUN test "$(go version | awk '{print $3}')" = go1.27.3 \
    && go mod download && go mod verify \
    && mkdir -p /opt/isolation \
    && go list -m -json all > /opt/isolation/modules.json \
    && dpkg-query -W > /opt/isolation/os-packages.txt
# Exact source is compiled only in the explicitly offline step below.
COPY backend /src/backend
# Exact public catalog fixture only; no broader frontend tree or credentials.
COPY frontend/src/constants/platformCatalog.builtin.json /src/frontend/src/constants/platformCatalog.builtin.json
COPY validation-receipt.json /src/validation-receipt.json
COPY source-manifest.json /opt/isolation/source-manifest.json
COPY isolated-unit-entrypoint.py /opt/isolation/isolated-unit-entrypoint.py
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
    --suite unit --source-root /src --manifest /opt/isolation/source-manifest.json \
    --manifest-sha256 "$SOURCE_MANIFEST_SHA256" --output /opt/isolation/compiled \
    && rm -rf /tmp/build-cache /tmp/build-tmp /tmp/home /tmp/gopath
