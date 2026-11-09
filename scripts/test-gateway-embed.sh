#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root/backend"

fixture_dir=internal/web/dist
fixture_file="$fixture_dir/index.html"
fixture_dir_created=false
fixture_file_created=false
cleanup_embed_fixture() {
  if [[ "$fixture_file_created" == true ]]; then rm -f -- "$fixture_file"; fi
  if [[ "$fixture_dir_created" == true ]]; then rmdir -- "$fixture_dir"; fi
}
trap cleanup_embed_fixture EXIT

if [[ ! -e "$fixture_file" && ! -L "$fixture_file" ]]; then
  if [[ -L "$fixture_dir" ]]; then
    printf '%s\n' 'Refusing to create an embed fixture in a symlinked dist directory' >&2
    exit 1
  fi
  if [[ ! -d "$fixture_dir" ]]; then
    mkdir -- "$fixture_dir"
    fixture_dir_created=true
  fi
  (set -o noclobber; : > "$fixture_file")
  fixture_file_created=true
  printf '%s\n' '<!doctype html><html><body>bridge routing fixture</body></html>' > "$fixture_file"
fi

test_name=TestBridgeForwardBypassesEmbeddedFrontend
go test -tags=unit,embed ./internal/web -list "^${test_name}$" | grep -Fx "$test_name"
go test -count=1 -tags=unit,embed ./internal/web -run "^${test_name}$"
