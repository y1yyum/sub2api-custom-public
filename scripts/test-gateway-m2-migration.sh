#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd -- "$repo_root/backend"
export GEMINI_CLI_OAUTH_CLIENT_ID=custom-test-client

if [[ ! -d internal/custom/scheduledowner || ! -d cmd/scheduled-owner ]]; then
  echo 'scheduled owner packages are missing' >&2
  exit 1
fi
shopt -s nullglob
owner_service_files=(internal/service/scheduled_test_owner*_test.go)
owner_integration_files=(internal/repository/scheduled_owner*_integration_test.go)
if (( ${#owner_service_files[@]} == 0 )) ||
  ! grep -Eq '^func Test[[:alnum:]_]*Owner[[:alnum:]_]*\(' "${owner_service_files[@]}"; then
  echo 'scheduled owner service tests are missing' >&2
  exit 1
fi
if (( ${#owner_integration_files[@]} == 0 )) ||
  ! grep -Eq '^func TestScheduledOwner[[:alnum:]_]*\(' "${owner_integration_files[@]}"; then
  echo 'scheduled owner integration tests are missing' >&2
  exit 1
fi

go test -race -count=1 -tags=unit ./internal/custom/scheduledowner/... ./internal/custom/dispatch/... ./cmd/scheduled-owner/... ./cmd/dispatchd
go test -race -count=1 -tags=unit -run '^Test(Scheduled|NormalizeScheduled|NextQuiz|CreatePlanWithQuestions|UpdatePlanWithQuestions|RunnerQuiz|RunnerSkipsQuiz|RunnerCharArt)' ./internal/service
go test -race -count=1 -tags=unit -run '^TestScheduledOwner' ./internal/repository
CI=true go test -race -count=1 -tags=integration -run '^TestScheduledOwner' ./internal/repository
go build ./cmd/scheduled-owner ./cmd/dispatchd ./cmd/server
