#!/usr/bin/env bash
# Full unit run. Tests in .gitea/slow-unit-tests.txt mostly wait on timers
# and run one after another in their package; here each listed package is
# compiled once and its binary runs the rest of the package beside
# UNIT_SLOW_SHARDS shards of the listed tests. Other packages run as usual.
set -euo pipefail

cd "$(dirname "$0")/../backend"
module=github.com/Wei-Shaw/sub2api
list=$(realpath ../.gitea/slow-unit-tests.txt)
shards=${UNIT_SLOW_SHARDS:-4}
bin=$(mktemp -d)
trap 'rm -rf "$bin"' EXIT

mapfile -t slow_packages < <(awk '!/^#/ && NF {print $1}' "$list" | sort -u)
go test -tags=unit -c -o "$bin/" "${slow_packages[@]/#/./}"
all_packages=$(go list -tags=unit ./...)
mapfile -t other_packages < <(grep -vxF -f <(printf "$module/%s\n" "${slow_packages[@]}") <<<"$all_packages")

run() {
  local label=$1; shift
  local start=$SECONDS status=0
  "$@" 2>&1 | sed -u "s|^|[$label] |" || status=$?
  if [ "$status" -eq 0 ]; then
    echo "ok   $label $((SECONDS - start))s"
  else
    echo "FAIL $label $((SECONDS - start))s"
  fi
  return "$status"
}

slow_pattern() {
  awk -v package="$1" -v shard="$2" -v shards="$3" \
    '!/^#/ && $1 == package && n++ % shards == shard {print $2}' "$list" | paste -sd'|'
}

pids=()
run others go test -tags=unit "${other_packages[@]}" & pids+=($!)
for package in "${slow_packages[@]}"; do
  test_binary="$bin/$(basename "$package").test"
  pattern=$(slow_pattern "$package" 0 1)
  (cd "$package" && run "$package rest" "$test_binary" -test.paniconexit0 -test.timeout=15m \
    -test.skip "^($pattern)\$") & pids+=($!)
  for ((shard = 0; shard < shards; shard++)); do
    pattern=$(slow_pattern "$package" "$shard" "$shards")
    [ -n "$pattern" ] || continue
    (cd "$package" && run "$package slow $((shard + 1))/$shards" "$test_binary" \
      -test.paniconexit0 -test.timeout=15m -test.run "^($pattern)\$") & pids+=($!)
  done
done

failed=0
for pid in "${pids[@]}"; do
  wait "$pid" || failed=1
done
exit "$failed"
