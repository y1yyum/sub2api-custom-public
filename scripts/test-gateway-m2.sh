#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root/backend"
export GEMINI_CLI_OAUTH_CLIENT_ID=custom-test-client

# Fail closed while a required test or command has not yet been integrated.
test -d cmd/dispatchd
test -f internal/repository/dispatch_integration_test.go
test -f internal/repository/bridge_topology_integration_test.go
test -f ../scripts/test-gateway-m2-strict.sh
test -f ../scripts/test-gateway-m2-observation.sh
dispatch_tests="$(sed -nE 's/^func (Test[[:alnum:]_]+)\(.*/\1/p' internal/repository/dispatch_integration_test.go | paste -sd '|' -)"
topology_tests="$(sed -nE 's/^func (Test[[:alnum:]_]+)\(.*/\1/p' internal/repository/bridge_topology_integration_test.go | paste -sd '|' -)"
test -n "$dispatch_tests"
test -n "$topology_tests"
[[ "|$topology_tests|" == *"|TestBridgeTopology|"* ]]
[[ "|$topology_tests|" == *"|TestBridgeTopologyDispatchProbe|"* ]]

go test -race -count=1 -tags=unit ./internal/custom/dispatch/... ./internal/custombridge/... ./cmd/dispatchd ./cmd/bridge-edge ./cmd/bridge-schema
shopt -s nullglob
bridge_unit_files=(internal/service/*bound_request*_test.go internal/service/*bridge_receipt*_test.go
  internal/service/openai_gateway_forward*_test.go internal/server/middleware/bridge*_test.go
  internal/server/bridge*_test.go internal/server/http_bridge_write_deadline_test.go)
bridge_unit_tests="$(sed -nE 's/^func (Test[[:alnum:]_]+)\(.*/\1/p' "${bridge_unit_files[@]}" | paste -sd '|' -)"
test -n "$bridge_unit_tests"
go test -race -count=1 -tags=unit ./internal/service ./internal/server/middleware ./internal/server -run "^($bridge_unit_tests)$"
CI=true go test -count=1 -tags=integration ./internal/repository -run '^(TestDispatch|TestBridgeTopology|TestBridgeTelemetry)'
bash ../scripts/test-gateway-m2-strict.sh
bash ../scripts/test-gateway-m2-observation.sh
go build ./cmd/dispatchd ./cmd/bridge-edge ./cmd/bridge-relay
