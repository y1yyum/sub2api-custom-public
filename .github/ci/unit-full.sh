#!/usr/bin/env bash
# 完整单元测试走同一隔离执行器；慢测试分片由源绑定计划控制。
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
plan=$1
output=$2
shift 2
python3 -c 'import json, sys; p=json.load(open(sys.argv[1])); assert p["mode"] == "full"' "$plan"
python3 "$root/.github/scripts/run-isolated-unit.py" --suite unit --plan "$plan" "$output" "$@"
