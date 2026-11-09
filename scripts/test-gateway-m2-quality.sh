#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd -- "$repo_root/backend"
export GEMINI_CLI_OAUTH_CLIENT_ID=custom-test-client

test -d internal/custom/quality
test -d cmd/quality-calibrate
shopt -s nullglob
quality_integration_files=(internal/repository/dispatch_quality*_integration_test.go)
test "${#quality_integration_files[@]}" -gt 0
grep -Eq '^func TestDispatchQuality[[:alnum:]_]*\(' "${quality_integration_files[@]}"

go test -race -count=1 -tags=unit ./internal/custom/dispatch/... ./internal/custom/quality/... ./cmd/dispatchd ./cmd/quality-calibrate/...
CI=true go test -race -count=1 -tags=integration -run '^TestDispatchQuality' ./internal/repository
go build ./cmd/dispatchd ./cmd/quality-calibrate
