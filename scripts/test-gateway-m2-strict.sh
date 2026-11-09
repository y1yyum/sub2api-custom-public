#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd -- "$repo_root/backend"
export CI=true
export GEMINI_CLI_OAUTH_CLIENT_ID=custom-test-client

test -f internal/repository/bridge_strict_topology_integration_test.go
strict_tests="$(sed -nE 's/^func (TestBridgeStrictTopology[[:alnum:]_]*)\(.*/\1/p' internal/repository/bridge_strict_topology_integration_test.go)"
test -n "$strict_tests"

# 独占合成拓扑运行真实 Core/Edge/relay；不重复 M0/M1 拓扑。
exec go test -p 1 -count=1 -tags=integration ./internal/repository \
  -run '^TestBridgeStrictTopology' -timeout 10m -v "$@"
