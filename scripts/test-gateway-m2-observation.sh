#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd -- "$repo_root/backend"
export GEMINI_CLI_OAUTH_CLIENT_ID=custom-test-client

test -f internal/repository/bridge_observation_topology_integration_test.go
observation_tests="$(sed -nE 's/^func (TestBridgeObservationTopology[[:alnum:]_]*)\(.*/\1/p' internal/repository/bridge_observation_topology_integration_test.go)"
test -n "$observation_tests"

CI=true go test -tags=integration ./internal/repository -run '^TestBridgeObservationTopology' "$@"
