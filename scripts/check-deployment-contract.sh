#!/usr/bin/env bash
# =============================================================================
# check-deployment-contract.sh — 校验部署契约不变量
# =============================================================================
# 目的：
#   部署契约（docs/DEPLOYMENT_CONTRACT.md）依赖若干代码/Dockerfile 不变量。
#   上游合并或二开改动可能无意破坏它们，而单元测试未必能发现。
#   本脚本把这些不变量变成 CI 可执行的断言。
#
# 检查项：
#   1. 默认监听地址为 0.0.0.0（不得为 127.0.0.1）
#   2. 默认监听端口为 8080
#   3. /health 路由已注册
#   4. Dockerfile EXPOSE 8080
#   5. Dockerfile 含 HEALTHCHECK 且指向 /health
#   6. Dockerfile 使用 /app/data 作为数据目录
#   7. Dockerfile 创建并使用非 root 用户
#   8. Dockerfile 未以 root 作为最终运行用户
#   9. 镜像内不存在硬编码的生产密钥
#
# 用法：./scripts/check-deployment-contract.sh
# 退出码：0 = 全部通过；1 = 有违反项
# =============================================================================

set -uo pipefail

cd "$(dirname "$0")/.." || exit 2

CONFIG_GO="backend/internal/config/config.go"
ROUTES_COMMON="backend/internal/server/routes/common.go"
DOCKERFILE="Dockerfile"

PASSED=0
FAILED=0

ok()  { PASSED=$((PASSED+1)); printf '  \033[32m✓\033[0m %s\n' "$*"; }
bad() { FAILED=$((FAILED+1)); printf '  \033[31m✗\033[0m %s\n' "$*" >&2; }

printf '\n部署契约校验（docs/DEPLOYMENT_CONTRACT.md）\n'

# --- 前置文件 ---------------------------------------------------------------
printf '\n[0] 前置文件\n'
for f in "$CONFIG_GO" "$ROUTES_COMMON" "$DOCKERFILE"; do
  if [ -f "$f" ]; then ok "$f 存在"; else bad "$f 缺失"; fi
done
if [ "$FAILED" -gt 0 ]; then
  printf '\ncheck-deployment-contract: 前置文件缺失，终止\n' >&2
  exit 1
fi

# --- 1. 监听地址 ------------------------------------------------------------
printf '\n[1] 监听地址必须为 0.0.0.0\n'
if grep -qE 'SetDefault\("server\.host",\s*"0\.0\.0\.0"\)' "$CONFIG_GO"; then
  ok 'server.host 默认值为 0.0.0.0'
else
  bad 'server.host 默认值不再是 0.0.0.0（契约要求 0.0.0.0，禁止 127.0.0.1）'
fi

# 显式禁止把默认监听改为回环地址
if grep -qE 'SetDefault\("server\.host",\s*"127\.0\.0\.1"\)' "$CONFIG_GO"; then
  bad 'server.host 默认值被改为 127.0.0.1 —— 会导致 Docker 端口映射失效'
fi

# --- 2. 监听端口 ------------------------------------------------------------
printf '\n[2] 监听端口必须为 8080\n'
if grep -qE 'SetDefault\("server\.port",\s*8080\)' "$CONFIG_GO"; then
  ok 'server.port 默认值为 8080'
else
  bad 'server.port 默认值不再是 8080'
fi

# --- 3. 健康检查路由 --------------------------------------------------------
printf '\n[3] /health 路由\n'
if grep -qE 'GET\("/health"' "$ROUTES_COMMON"; then
  ok 'GET /health 已注册'
else
  bad 'GET /health 未在 routes/common.go 中注册'
fi

# --- 4. EXPOSE --------------------------------------------------------------
printf '\n[4] Dockerfile EXPOSE\n'
if grep -qE '^EXPOSE[[:space:]]+8080' "$DOCKERFILE"; then
  ok 'Dockerfile EXPOSE 8080'
else
  bad 'Dockerfile 未 EXPOSE 8080'
fi

# --- 5. HEALTHCHECK ---------------------------------------------------------
printf '\n[5] Dockerfile HEALTHCHECK\n'
if grep -qE '^HEALTHCHECK' "$DOCKERFILE"; then
  if grep -A3 '^HEALTHCHECK' "$DOCKERFILE" | grep -q '/health'; then
    ok 'HEALTHCHECK 指向 /health'
  else
    bad 'HEALTHCHECK 未指向 /health'
  fi
else
  bad 'Dockerfile 缺少 HEALTHCHECK'
fi

# --- 6. 数据目录 ------------------------------------------------------------
printf '\n[6] 数据持久化目录 /app/data\n'
if grep -q '/app/data' "$DOCKERFILE"; then
  ok 'Dockerfile 使用 /app/data'
else
  bad 'Dockerfile 未使用 /app/data 作为数据目录'
fi

# --- 7. 非 root 用户 --------------------------------------------------------
printf '\n[7] 非 root 运行\n'
if grep -qE 'adduser|useradd' "$DOCKERFILE"; then
  ok 'Dockerfile 创建了非 root 用户'
else
  bad 'Dockerfile 未创建非 root 用户'
fi
if grep -qE '^USER[[:space:]]+(root|0)\s*$' "$DOCKERFILE"; then
  bad 'Dockerfile 最终以 root 用户运行'
else
  ok 'Dockerfile 未将最终运行用户设为 root'
fi

# --- 8. 无内嵌生产密钥 ------------------------------------------------------
printf '\n[8] 镜像内不得内嵌真实密钥\n'
# 只在 Dockerfile 的 ENV/ARG 指令中查找可疑的字面量密钥赋值
SUSPECT="$(grep -nE '^[[:space:]]*(ENV|ARG)[[:space:]]+[A-Z_]*(SECRET|PASSWORD|TOKEN|API_KEY|PRIVATE_KEY)[A-Z_]*=[^[:space:]]+' "$DOCKERFILE" \
  | grep -vE '=\s*$|\=\$\{|^\s*#' || true)"
if [ -z "$SUSPECT" ]; then
  ok 'Dockerfile 未内嵌硬编码密钥'
else
  bad 'Dockerfile 疑似内嵌硬编码密钥：'
  printf '%s\n' "$SUSPECT" | sed 's/^/      /' >&2
fi

# --- 结论 -------------------------------------------------------------------
printf '\n契约校验结果：通过 %d 项，失败 %d 项\n' "$PASSED" "$FAILED"
if [ "$FAILED" -gt 0 ]; then
  printf '\ncheck-deployment-contract: 失败 —— 若为有意的契约变更，请先开 Issue 并更新 docs/DEPLOYMENT_CONTRACT.md\n' >&2
  exit 1
fi
printf 'check-deployment-contract: 全部通过\n'
