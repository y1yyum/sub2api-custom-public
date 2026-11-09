#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root/backend"
export GEMINI_CLI_OAUTH_CLIENT_ID=custom-test-client

go test -count=1 -tags=unit ./internal/custombridge/... ./cmd/bridge-edge ./cmd/bridge-relay ./cmd/bridge-schema
go test -count=1 -tags=unit ./internal/service ./internal/handler ./internal/server ./internal/server/middleware \
  -run 'BoundRequest|BridgeForward|BridgeReceipt|UsageRecordTask|CyberPolicy|CustomCanceledResponses'

bash "$repo_root/scripts/test-gateway-embed.sh"

CI=true go test -count=1 -tags=integration ./internal/repository \
  -run 'BridgeForward|BridgeRelay|BridgeReceipt|BridgeCrash|UsageBillingRepositoryApply'
go build ./...
