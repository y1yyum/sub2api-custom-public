#!/usr/bin/env bash
# =============================================================================
# test-smoke.sh — 对已构建镜像执行端到端冒烟测试
# =============================================================================
# 设计目标：
#   - 在隔离的临时环境中验证镜像真实可运行（而非仅检查文件存在）
#   - 使用临时 PostgreSQL + Redis，不接触任何生产数据
#   - 使用测试专用管理员账户与随机密钥，不使用真实上游凭据
#   - 覆盖：首次初始化 / 全量迁移 / 健康检查 / 登录鉴权 / API Key 鉴权 /
#           容器重启后数据持久性
#
# 用法：
#   IMAGE=ghcr.io/y1yyum/sub2api-custom-public@sha256:<DIGEST> .github/scripts/test-smoke.sh
#   IMAGE=sub2api-custom:local .github/scripts/test-smoke.sh
#
# 环境变量：
#   IMAGE          必填，被测镜像引用
#   KEEP_ON_FAIL   设为 1 时失败不清理容器，便于排查
#   SMOKE_BIND_HOST 发布端口绑定及探测的宿主地址，默认 127.0.0.1；
#                  通过 DOCKER_HOST 使用远端/隔离 daemon 时设为其可达地址
#
# 退出码：0 = 全部通过；1 = 有断言失败；2 = 环境/用法错误
# =============================================================================

set -euo pipefail

IMAGE="${IMAGE:-}"
BIND_HOST="${SMOKE_BIND_HOST:-127.0.0.1}"
if [ -z "$IMAGE" ]; then
  printf 'test-smoke: 必须通过环境变量 IMAGE 指定被测镜像\n' >&2
  exit 2
fi

for bin in docker curl; do
  command -v "$bin" >/dev/null 2>&1 || { printf 'test-smoke: 缺少依赖 %s\n' "$bin" >&2; exit 2; }
done

# ---------------------------------------------------------------------------
# 测试参数（全部为临时值，不涉及任何生产凭据）
# ---------------------------------------------------------------------------
SUFFIX="$$-$(date +%s)"
NETWORK="sub2api-smoke-net-${SUFFIX}"
PG_NAME="sub2api-smoke-pg-${SUFFIX}"
REDIS_NAME="sub2api-smoke-redis-${SUFFIX}"
APP_NAME="sub2api-smoke-app-${SUFFIX}"

PG_USER="smoke"
PG_PASSWORD="smoke-pg-password-$(openssl rand -hex 8 2>/dev/null || echo fallback)"
PG_DB="sub2api_smoke"
JWT_SECRET="$(openssl rand -hex 32 2>/dev/null || echo 0000000000000000000000000000000000000000000000000000000000000000)"
TOTP_KEY="$(openssl rand -hex 32 2>/dev/null || echo 1111111111111111111111111111111111111111111111111111111111111111)"
ADMIN_EMAIL="smoke-admin@example.invalid"
ADMIN_PASSWORD="Smoke-Admin-Passw0rd!$(openssl rand -hex 4 2>/dev/null || echo x)"

PASSED=0
FAILED=0

log()  { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
info() { printf '    %s\n' "$*"; }
ok()   { PASSED=$((PASSED+1)); printf '    \033[32m✓\033[0m %s\n' "$*"; }
bad()  { FAILED=$((FAILED+1)); printf '    \033[31m✗\033[0m %s\n' "$*" >&2; }

cleanup() {
  local code=$?
  if [ "$code" -ne 0 ] && [ "${KEEP_ON_FAIL:-0}" = "1" ]; then
    printf '\ntest-smoke: 失败且 KEEP_ON_FAIL=1，保留容器供排查：%s %s %s\n' \
      "$APP_NAME" "$PG_NAME" "$REDIS_NAME" >&2
    return
  fi
  log "清理临时资源"
  docker rm -f "$APP_NAME" "$PG_NAME" "$REDIS_NAME" >/dev/null 2>&1 || true
  docker network rm "$NETWORK" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
log "准备隔离网络与依赖服务"
docker network create "$NETWORK" >/dev/null
info "网络 $NETWORK"

docker run -d --name "$PG_NAME" --network "$NETWORK" \
  -e POSTGRES_USER="$PG_USER" \
  -e POSTGRES_PASSWORD="$PG_PASSWORD" \
  -e POSTGRES_DB="$PG_DB" \
  postgres:18-alpine >/dev/null
info "PostgreSQL 已启动"

docker run -d --name "$REDIS_NAME" --network "$NETWORK" \
  redis:8-alpine >/dev/null
info "Redis 已启动"

# 等待 PostgreSQL 就绪。必须走 TCP：官方镜像首次初始化时会先启动一个
# 只监听 Unix 套接字的临时实例，套接字检查会在正式实例监听 TCP 之前通过。
log "等待 PostgreSQL 就绪"
pg_ready=0
for _ in $(seq 1 60); do
  if docker exec "$PG_NAME" pg_isready -h 127.0.0.1 -U "$PG_USER" -d "$PG_DB" >/dev/null 2>&1; then
    pg_ready=1
    break
  fi
  sleep 2
done
if [ "$pg_ready" -ne 1 ]; then
  printf 'test-smoke: PostgreSQL 未能在 120s 内就绪\n' >&2
  docker logs "$PG_NAME" 2>&1 | tail -30 >&2
  exit 1
fi
ok "PostgreSQL 就绪"

# ---------------------------------------------------------------------------
log "启动被测应用容器"
docker run -d --name "$APP_NAME" --network "$NETWORK" \
  -p "${BIND_HOST}::8080" \
  -e AUTO_SETUP=true \
  -e SERVER_HOST=0.0.0.0 \
  -e SERVER_PORT=8080 \
  -e SERVER_MODE=release \
  -e DATABASE_HOST="$PG_NAME" \
  -e DATABASE_PORT=5432 \
  -e DATABASE_USER="$PG_USER" \
  -e DATABASE_PASSWORD="$PG_PASSWORD" \
  -e DATABASE_DBNAME="$PG_DB" \
  -e DATABASE_SSLMODE=disable \
  -e REDIS_HOST="$REDIS_NAME" \
  -e REDIS_PORT=6379 \
  -e REDIS_DB=0 \
  -e JWT_SECRET="$JWT_SECRET" \
  -e TOTP_ENCRYPTION_KEY="$TOTP_KEY" \
  -e ADMIN_EMAIL="$ADMIN_EMAIL" \
  -e ADMIN_PASSWORD="$ADMIN_PASSWORD" \
  -e TZ=UTC \
  "$IMAGE" >/dev/null

HOST_PORT="$(docker port "$APP_NAME" 8080/tcp 2>/dev/null | head -1 | sed 's/.*://' || true)"
if [ -z "$HOST_PORT" ]; then
  printf 'test-smoke: 无法解析映射端口（容器状态：%s）\n' \
    "$(docker inspect -f '{{.State.Status}} exit={{.State.ExitCode}}' "$APP_NAME" 2>/dev/null)" >&2
  docker logs "$APP_NAME" 2>&1 | tail -50 >&2
  exit 1
fi
BASE="http://${BIND_HOST}:${HOST_PORT}"
info "应用地址 $BASE（容器内 0.0.0.0:8080）"

# ---------------------------------------------------------------------------
log "断言 1：健康检查 /health"
health_ok=0
for _ in $(seq 1 90); do
  if curl -fsS --max-time 5 "$BASE/health" >/dev/null 2>&1; then
    health_ok=1
    break
  fi
  if [ "$(docker inspect -f '{{.State.Running}}' "$APP_NAME" 2>/dev/null)" != "true" ]; then
    printf 'test-smoke: 应用容器已退出\n' >&2
    docker logs "$APP_NAME" 2>&1 | tail -50 >&2
    exit 1
  fi
  sleep 2
done
if [ "$health_ok" -eq 1 ]; then
  BODY="$(curl -fsS --max-time 5 "$BASE/health")"
  ok "GET /health → 200，body: $BODY"
  case "$BODY" in
    *'"status"'*'"ok"'*) ok "健康响应含 status=ok" ;;
    *) bad "健康响应缺少 status=ok：$BODY" ;;
  esac
else
  bad "GET /health 未在 180s 内返回成功"
  docker logs "$APP_NAME" 2>&1 | tail -50 >&2
fi

# ---------------------------------------------------------------------------
log "断言 2：初始化状态 /setup/status"
CODE="$(curl -s -o /tmp/smoke_setup.json -w '%{http_code}' --max-time 10 "$BASE/setup/status" || echo 000)"
if [ "$CODE" = "200" ]; then
  ok "GET /setup/status → 200（首次初始化已完成）"
else
  bad "GET /setup/status 期望 200，实际 $CODE"
fi

# ---------------------------------------------------------------------------
log "断言 3：全量数据库迁移已执行"
MIG_COUNT="$(docker exec "$PG_NAME" psql -U "$PG_USER" -d "$PG_DB" -tAc \
  "SELECT count(*) FROM schema_migrations" 2>/dev/null || echo 0)"
if [ "${MIG_COUNT:-0}" -gt 100 ]; then
  ok "schema_migrations 记录数 = $MIG_COUNT（迁移已全量应用）"
else
  bad "schema_migrations 记录数异常：$MIG_COUNT"
fi

# ---------------------------------------------------------------------------
log "断言 4：管理员登录（证明 DB + 迁移 + 密码哈希 + JWT + Redis 链路可用）"
LOGIN_CODE="$(curl -s -o /tmp/smoke_login.json -w '%{http_code}' --max-time 20 \
  -X POST "$BASE/api/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}" || echo 000)"
if [ "$LOGIN_CODE" = "200" ]; then
  ok "POST /api/v1/auth/login → 200"
  if grep -qi 'token' /tmp/smoke_login.json; then
    ok "登录响应包含 token"
  else
    bad "登录响应未包含 token：$(head -c 300 /tmp/smoke_login.json)"
  fi
else
  bad "POST /api/v1/auth/login 期望 200，实际 $LOGIN_CODE：$(head -c 300 /tmp/smoke_login.json 2>/dev/null)"
fi

# 错误口令必须被拒绝
BAD_CODE="$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 \
  -X POST "$BASE/api/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"definitely-wrong-password\"}" || echo 000)"
case "$BAD_CODE" in
  401|400|403) ok "错误口令被拒绝（HTTP $BAD_CODE）" ;;
  *)           bad "错误口令未被拒绝，HTTP $BAD_CODE" ;;
esac

# ---------------------------------------------------------------------------
log "断言 5：API Key 鉴权对未认证请求强制生效"
GW_CODE="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$BASE/v1/models" || echo 000)"
case "$GW_CODE" in
  401|403) ok "GET /v1/models 无 Key → HTTP $GW_CODE（鉴权生效）" ;;
  200)     bad "GET /v1/models 无 Key 竟返回 200（鉴权失效）" ;;
  *)       bad "GET /v1/models 无 Key 返回意外状态码 $GW_CODE" ;;
esac

ADMIN_CODE="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$BASE/api/v1/admin/users" || echo 000)"
case "$ADMIN_CODE" in
  401|403) ok "GET /api/v1/admin/users 无 Token → HTTP $ADMIN_CODE（管理员鉴权生效）" ;;
  200)     bad "GET /api/v1/admin/users 无 Token 竟返回 200（管理员鉴权失效）" ;;
  *)       bad "GET /api/v1/admin/users 无 Token 返回意外状态码 $ADMIN_CODE" ;;
esac

# ---------------------------------------------------------------------------
log "断言 6：容器重启后数据持久性"
OLD_PORT="$HOST_PORT"
docker restart "$APP_NAME" >/dev/null

# (a) 等待容器回到 running
running=0
for _ in $(seq 1 30); do
  if [ "$(docker inspect -f '{{.State.Running}}' "$APP_NAME" 2>/dev/null || echo false)" = "true" ]; then
    running=1
    break
  fi
  sleep 1
done
if [ "$running" -eq 1 ]; then
  ok "重启后容器回到 running"
else
  bad "重启后容器未回到 running"
  docker inspect -f 'status={{.State.Status}} exit={{.State.ExitCode}}' "$APP_NAME" >&2 || true
fi

# (b) 重新解析宿主端口，不能复用重启前的 BASE。
#
# 实测（运行 38631872335）：重启后容器内应用已监听 0.0.0.0:8080 并持续运行
# （容器日志可见 "Server started on 0.0.0.0:8080" 及其后 2 分钟的业务日志），
# 但宿主侧按旧端口探测连续 180s 全部连接失败（HTTP 000000）。
# 根因是 `-p 127.0.0.1::8080` 用的是**临时宿主端口**，容器重启后该端口可能被
# 重新分配，因此必须重新解析并更新探测地址。
NEW_PORT="$(docker port "$APP_NAME" 8080/tcp 2>/dev/null | head -1 | sed 's/.*://')"
if [ -z "$NEW_PORT" ]; then
  bad "重启后无法解析宿主端口"
else
  if [ "$NEW_PORT" != "$OLD_PORT" ]; then
    info "宿主端口在重启后变化：$OLD_PORT → $NEW_PORT（已更新探测地址）"
  else
    info "宿主端口未变化：$NEW_PORT"
  fi
  HOST_PORT="$NEW_PORT"
  BASE="http://${BIND_HOST}:${HOST_PORT}"
fi

# (c) 宿主侧健康检查
restart_ok=0
for _ in $(seq 1 90); do
  if curl -fsS --max-time 5 "$BASE/health" >/dev/null 2>&1; then
    restart_ok=1
    break
  fi
  sleep 2
done

# (d) 宿主侧失败时，用容器内探针区分「应用没起来」与「宿主端口映射失效」。
#     镜像基于 Alpine，自带 busybox wget（与 Dockerfile HEALTHCHECK 同款）。
if [ "$restart_ok" -eq 1 ]; then
  ok "重启后 /health 恢复 200"
else
  bad "重启后 /health 未恢复"
  in_container="no"
  if docker exec "$APP_NAME" wget -q -T 5 -O /dev/null http://127.0.0.1:8080/health 2>/dev/null; then
    in_container="yes"
  fi
  info "容器内自检 /health：$in_container（yes=应用正常，问题出在宿主端口映射）"
  info "宿主端口映射：$(docker port "$APP_NAME" 2>/dev/null | tr '\n' ' ')"
  docker ps -a --filter "name=^${APP_NAME}$" >&2 || true
  docker logs "$APP_NAME" 2>&1 | tail -50 >&2
fi

RESTART_LOGIN_CODE="$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 \
  -X POST "$BASE/api/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}" || echo 000)"
if [ "$RESTART_LOGIN_CODE" = "200" ]; then
  ok "重启后仍可用原账户登录（数据持久化有效）"
else
  bad "重启后登录失败，HTTP $RESTART_LOGIN_CODE"
fi

RESTART_MIG="$(docker exec "$PG_NAME" psql -U "$PG_USER" -d "$PG_DB" -tAc \
  "SELECT count(*) FROM schema_migrations" 2>/dev/null || echo 0)"
if [ "$RESTART_MIG" = "$MIG_COUNT" ]; then
  ok "重启后迁移记录数不变（$RESTART_MIG，迁移幂等）"
else
  bad "重启后迁移记录数变化：$MIG_COUNT → $RESTART_MIG"
fi

# ---------------------------------------------------------------------------
log "冒烟测试结果"
printf '    通过 %d 项，失败 %d 项\n' "$PASSED" "$FAILED"
if [ "$FAILED" -gt 0 ]; then
  printf '\ntest-smoke: 失败\n' >&2
  exit 1
fi
printf '\ntest-smoke: 全部通过\n'
