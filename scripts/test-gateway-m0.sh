#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root/backend"

export GEMINI_CLI_OAUTH_CLIENT_ID=custom-test-client

go test -count=1 -tags=unit ./internal/custombridge
go test -count=1 -tags=unit ./internal/service ./internal/handler ./internal/repository \
  -run 'BoundRequest|BridgeReceipt|UsageBilling|UsageRecordTask|CyberPolicy|CustomCanceledResponses'
CI=true go test -count=1 -tags=integration ./internal/repository \
  -run 'BridgeReceipt|BridgeCrash|UsageBillingRepositoryApply'
