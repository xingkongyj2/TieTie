#!/usr/bin/env bash
set -euo pipefail

NETWORK_NAME="${NETWORK_NAME:-howbuyyou_net}"
CONTAINER_NAME="${CONTAINER_NAME:-tietie}"
IMAGE="${IMAGE:-docker.cnb.cool/xingkong/my/tietie/tietie:1}"
PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ENV_FILE:-${PROJECT_DIR}/backend/.env.local}"
LOG_DIR="${LOG_DIR:-${PROJECT_DIR}/backend/logs}"
HOST_PORT="${HOST_PORT:-4173}"

if ! command -v docker >/dev/null 2>&1; then
  echo "未找到 Docker；请先运行 deploy/deploy_docker.sh。" >&2
  exit 1
fi
if [[ ! -f "$ENV_FILE" ]]; then
  echo "缺少配置文件 $ENV_FILE；请复制 backend/.env.example 并填写生产环境配置。" >&2
  exit 1
fi
if ! grep -Eq '^QODER_ACCESS_TOKEN=.+$' "$ENV_FILE" || \
   ! grep -Eq '^MYSQL_HOST=.+$' "$ENV_FILE" || \
   ! grep -Eq '^JWT_SECRET=.+$' "$ENV_FILE"; then
  echo "请在 $ENV_FILE 中配置 QODER_ACCESS_TOKEN、MYSQL_HOST 和 JWT_SECRET。" >&2
  exit 1
fi

weather_args=()
if [[ -n "${QWEATHER_PRIVATE_KEY_HOST_FILE:-}" ]]; then
  if [[ ! -f "$QWEATHER_PRIVATE_KEY_HOST_FILE" ]]; then
    echo "和风天气私钥文件不存在：$QWEATHER_PRIVATE_KEY_HOST_FILE" >&2
    exit 1
  fi
  weather_args=(-v "${QWEATHER_PRIVATE_KEY_HOST_FILE}:/app/qweather_private.pem:ro" -e QWEATHER_PRIVATE_KEY_FILE=/app/qweather_private.pem)
fi

if ! docker network inspect "$NETWORK_NAME" >/dev/null 2>&1; then
  docker network create "$NETWORK_NAME"
fi

# 私有镜像需要 CNB 访问令牌；已有 docker login 会话可直接复用。
if [[ -n "${CNB_TOKEN:-}" ]]; then
  printf '%s' "$CNB_TOKEN" | docker login docker.cnb.cool -u "${CNB_TOKEN_USER_NAME:-cnb}" --password-stdin
fi

docker pull "$IMAGE"
mkdir -p "$LOG_DIR"
if docker container inspect "$CONTAINER_NAME" >/dev/null 2>&1; then
  docker rm -f "$CONTAINER_NAME"
fi
docker run -d \
  --name "$CONTAINER_NAME" \
  --network "$NETWORK_NAME" \
  -p "${HOST_PORT}:4173" \
  --env-file "$ENV_FILE" \
  -e HOST=0.0.0.0 \
  -e PORT=4173 \
  -e STATIC_DIR=/app/dist \
  -e LOG_DIR=/app/logs \
  -v "${LOG_DIR}:/app/logs" \
  "${weather_args[@]}" \
  --restart unless-stopped \
  "$IMAGE"
echo "TieTie 已部署：${CONTAINER_NAME}，端口 ${HOST_PORT}。"
