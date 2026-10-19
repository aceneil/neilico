#!/usr/bin/env bash
# NEILICO 公网服务器一键部署（Debian/Ubuntu 等，已装 docker + compose v2）
# 用法：
#   export DOMAIN=neilico.example.com        # 必填：控制面域名（A 记录已指向本机）
#   export ADMIN_EMAIL=admin@example.com     # 必填：管理员邮箱
#   export ACME_EMAIL=ops@example.com        # 可选：证书通知邮箱
#   sudo -E bash deploy-public.sh
set -euo pipefail

: "${DOMAIN:?请先 export DOMAIN=你的域名}"
: "${ADMIN_EMAIL:?请先 export ADMIN_EMAIL=管理员邮箱}"
ACME_EMAIL="${ACME_EMAIL:-$ADMIN_EMAIL}"
ROOT="${ROOT:-/opt/neilico}"

mkdir -p "$ROOT"/data/{pg,rustdesk,downloads,caddy/data,caddy/config}
cd "$ROOT"

# ── 环境变量（密钥自动生成）─────────────────────────────────────────────────
umask 077
if [ ! -f neilico.env ]; then
  DB_PASS=$(openssl rand -hex 24)
  JWT=$(openssl rand -hex 32)
  ADMIN_PASS=$(openssl rand -base64 24 | tr -d '/+=' | cut -c1-24)
  cat > neilico.env <<EOF
POSTGRES_DB=neilico
POSTGRES_USER=neilico
POSTGRES_PASSWORD=$DB_PASS

NEILICO_AUTH_JWT_SECRET=$JWT
NEILICO_BOOTSTRAP_ADMIN_EMAIL=$ADMIN_EMAIL
NEILICO_BOOTSTRAP_ADMIN_PASSWORD=$ADMIN_PASS
NEILICO_BOOTSTRAP_DEFAULT_TENANT=default

NEILICO_ENROLL_PUBLIC_URL=https://$DOMAIN
NEILICO_ENROLL_AGENT_IMAGE=ghcr.io/aceneil/neilico-agent:latest

NEILICO_SERVER_HOST=0.0.0.0
NEILICO_LOG_LEVEL=info
NEILICO_LOG_FORMAT=json

NEILICO_PROXY_KIND=builtin
NEILICO_PROXY_LISTEN=:8081
NEILICO_PROXY_TLS_LISTEN=:8443

NEILICO_STREAM_PORT_MIN=20000
NEILICO_STREAM_PORT_MAX=20019

NEILICO_RD_ID_SERVER=$DOMAIN
NEILICO_RD_RELAY_SERVER=$DOMAIN
NEILICO_RD_KEY_DIR=/var/lib/neilico/rustdesk
NEILICO_RD_SERVER_MODE=on_demand
NEILICO_RD_IDLE_TIMEOUT=10m
EOF
  echo "已生成 neilico.env（管理员密码可用: grep NEILICO_BOOTSTRAP_ADMIN_PASSWORD neilico.env）"
else
  echo "neilico.env 已存在，保持不变"
fi

# ── Caddyfile ──────────────────────────────────────────────────────────────
cat > Caddyfile <<EOF
{
	email $ACME_EMAIL
}

$DOMAIN {
	encode zstd gzip
	reverse_proxy server:8080
}

# 业务域名示例（要几个写几段）：
# app.example.com {
# 	encode zstd gzip
# 	reverse_proxy server:8081
# }
EOF

# ── compose ────────────────────────────────────────────────────────────────
cat > docker-compose.yml <<'YAML'
name: neilico

x-logging: &default-logging
  driver: json-file
  options:
    max-size: "10m"
    max-file: "3"

services:
  server:
    image: ghcr.io/aceneil/neilico-server:latest
    container_name: neilico
    restart: unless-stopped
    env_file: [./neilico.env]
    environment:
      PGDATA: /var/lib/postgresql/data
      NEILICO_DATABASE_DRIVER: postgres
      NEILICO_SERVER_HOST: 0.0.0.0
      NEILICO_SERVER_DASHBOARD_DIR: /opt/neilico/dashboard
      NEILICO_SERVER_DASHBOARD_SPA: "true"
      NEILICO_DOWNLOADS_DIR: /usr/local/share/neilico/downloads
      NEILICO_BOOTSTRAP_ENV_FILE: /opt/neilico/bootstrap.env
      NEILICO_PROXY_LISTEN: ":8081"
      NEILICO_STREAM_PORT_MIN: "20000"
      NEILICO_STREAM_PORT_MAX: "20019"
    expose:
      - "8080"
      - "8081"
    ports:
      - "20000-20019:20000-20019/tcp"
      - "20000-20019:20000-20019/udp"
      - "21115:21115/tcp"
      - "21116:21116/tcp"
      - "21116:21116/udp"
      - "21117:21117/tcp"
      - "21118:21118/tcp"
      - "21119:21119/tcp"
    volumes:
      - ./data/pg:/var/lib/postgresql/data
      - ./neilico.env:/opt/neilico/bootstrap.env
      - ./data/rustdesk:/var/lib/neilico/rustdesk
      - ./data/downloads:/usr/local/share/neilico/downloads
    healthcheck:
      test: ["CMD-SHELL", "curl -fsS http://127.0.0.1:8080/healthz >/dev/null"]
      interval: 10s
      timeout: 3s
      retries: 10
      start_period: 40s
    logging: *default-logging

  caddy:
    image: caddy:2-alpine
    container_name: neilico-caddy
    restart: unless-stopped
    depends_on:
      server:
        condition: service_healthy
    ports:
      - "80:80"
      - "443:443"
      - "443:443/udp"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - ./data/caddy/data:/data
      - ./data/caddy/config:/config
    logging: *default-logging
YAML

docker compose pull
docker compose up -d
echo
echo "等待控制面就绪…"
for i in $(seq 1 30); do
  if curl -fsS "https://$DOMAIN/healthz" >/dev/null 2>&1; then
    echo "✅ https://$DOMAIN/healthz 正常"
    break
  fi
  sleep 4
done
docker compose ps
echo
echo "管理员密码：grep NEILICO_BOOTSTRAP_ADMIN_PASSWORD $ROOT/neilico.env"
