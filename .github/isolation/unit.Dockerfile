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
# COPY only; no source command, test binary, package init or TestMain executes.
COPY backend /src/backend
COPY validation-receipt.json /src/validation-receipt.json
COPY source-manifest.json /opt/isolation/source-manifest.json
COPY isolated-unit-entrypoint.py /opt/isolation/isolated-unit-entrypoint.py
WORKDIR /src/backend
USER 65532:65532
