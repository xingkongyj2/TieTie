#!/usr/bin/env bash
set -euo pipefail

NETWORK_NAME="${NETWORK_NAME:-howbuyyou_net}"
CONTAINER_NAME="${CONTAINER_NAME:-tietie}"
IMAGE="${IMAGE:-docker.cnb.cool/xingkong/my/tietie/tietie:1}"
PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ENV_FILE:-${PROJECT_DIR}/backend/.env.local}"
LOG_DIR="${LOG_DIR:-${PROJECT_DIR}/backend/logs}"
HOST_PORT="${HOST_PORT:-4173}"

# 本机 MySQL：MYSQL_LOCAL=true 时在本机起一个 MySQL 容器，并让应用连它。
# 建库建表由应用启动时自动完成（CREATE DATABASE IF NOT EXISTS + AutoMigrate），无需数据迁移。
MYSQL_LOCAL="${MYSQL_LOCAL:-true}"
MYSQL_CONTAINER="${MYSQL_CONTAINER:-tietie-mysql}"
MYSQL_IMAGE="${MYSQL_IMAGE:-mysql:8.0}"
MYSQL_DATA_VOLUME="${MYSQL_DATA_VOLUME:-tietie_mysql_data}"

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

env_get() { grep -E "^$1=" "$ENV_FILE" | head -1 | sed -E "s/^$1=//"; }

# 本机 MySQL：确保容器在 $NETWORK_NAME 上运行并等待就绪，让应用按容器名连它。
mysql_host_args=()
if [[ "$MYSQL_LOCAL" == "true" ]]; then
  MYSQL_ROOT_PW="$(env_get MYSQL_PASSWORD)"
  if [[ -z "$MYSQL_ROOT_PW" ]]; then
    echo "MYSQL_LOCAL=true 需要在 $ENV_FILE 设置 MYSQL_PASSWORD（作为本机 MySQL 的 root 密码）。" >&2
    exit 1
  fi
  if docker container inspect "$MYSQL_CONTAINER" >/dev/null 2>&1; then
    docker container start "$MYSQL_CONTAINER" >/dev/null 2>&1 || true
  else
    docker pull "$MYSQL_IMAGE"
    docker run -d --name "$MYSQL_CONTAINER" --network "$NETWORK_NAME" \
      -e MYSQL_ROOT_PASSWORD="$MYSQL_ROOT_PW" \
      -v "${MYSQL_DATA_VOLUME}:/var/lib/mysql" \
      --restart unless-stopped \
      "$MYSQL_IMAGE" --character-set-server=utf8mb4 --collation-server=utf8mb4_bin
    echo "已创建本机 MySQL 容器 ${MYSQL_CONTAINER}。"
  fi
  echo "等待 MySQL 就绪…"
  mysql_ready=""
  for _ in $(seq 1 60); do
    if docker exec "$MYSQL_CONTAINER" mysqladmin ping -h 127.0.0.1 -uroot -p"$MYSQL_ROOT_PW" --silent >/dev/null 2>&1; then mysql_ready=1; break; fi
    sleep 2
  done
  if [[ -z "$mysql_ready" ]]; then
    echo "本机 MySQL 120s 内未就绪；请 docker logs $MYSQL_CONTAINER 排查。" >&2
    exit 1
  fi
  mysql_host_args=(-e MYSQL_HOST="$MYSQL_CONTAINER")
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
  "${mysql_host_args[@]}" \
  -v "${LOG_DIR}:/app/logs" \
  "${weather_args[@]}" \
  --restart unless-stopped \
  "$IMAGE"
echo "TieTie 已部署：${CONTAINER_NAME}，端口 ${HOST_PORT}。"
