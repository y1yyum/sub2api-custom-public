#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd -- "$repo_root/backend"

# 本测试创建独占容器并构建实际 Core/Edge/relay；无 Docker 必须失败。
export CI=true
exec go test -p 1 -count=1 -tags=integration ./internal/repository \
  -run '^TestBridgeTopology($|DispatchProbe$)' -timeout 12m -v "$@"
