#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROJECT="neilico-v1f"
COMPOSE=(docker compose --project-name "$PROJECT"
  -f "$ROOT/deploy/docker-compose/docker-compose.yml"
  -f "$ROOT/deploy/docker-compose/docker-compose.tls.yml")
OUT_DIR="$ROOT/dashboard/screenshots"
RUN_DIR="$(mktemp -d "${TMPDIR:-/tmp}/neilico-v1f-dashboard.XXXXXX")"
PROFILE_BASE="$RUN_DIR/chrome-profile"
CHROME="/usr/bin/google-chrome"
BASE_URL="${NEILICO_V1F_BASE_URL:-http://127.0.0.1:23000}"
CONTROL_HTTP_PORT=28080
CONTROL_HTTPS_PORT=28443
PROXY_HTTP_PORT=28081
PROXY_HTTPS_PORT=28444
DASHBOARD_PORT=23000
POSTGRES_PORT=25432
REDIS_PORT=26379
NATS_PORT=24222
DEBUG_PORT=29222

export POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-$(openssl rand -hex 24)}"
export NEILICO_JWT_SECRET="${NEILICO_JWT_SECRET:-$(openssl rand -hex 32)}"
export BOOTSTRAP_ADMIN_EMAIL="${BOOTSTRAP_ADMIN_EMAIL:-admin@v1f.local}"
export BOOTSTRAP_ADMIN_PASSWORD="${BOOTSTRAP_ADMIN_PASSWORD:-$(openssl rand -hex 18)}"
export TLS_API_HTTP_PORT="$CONTROL_HTTP_PORT"
export TLS_API_HTTPS_PORT="$CONTROL_HTTPS_PORT"
export TLS_PROXY_HTTP_PORT="$PROXY_HTTP_PORT"
export TLS_PROXY_HTTPS_PORT="$PROXY_HTTPS_PORT"
export TLS_SMOKE_DIR="$RUN_DIR"
export COMPOSE_NO_ANSI=1
export DASHBOARD_PORT
export POSTGRES_PORT
export REDIS_PORT
export NATS_PORT

log() { printf '%s\n' "$*"; }
fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

assert_port_free() {
  local port="$1"
  if ss -ltnH "sport = :$port" 2>/dev/null | grep -q .; then
    fail "port $port is already in use; refusing to touch an existing service"
  fi
}

cleanup() {
  local rc=$?
  if [[ "${KEEP_NEILICO_V1F_STACK:-0}" != "1" ]]; then
    "${COMPOSE[@]}" --profile disabled down -v --remove-orphans >/dev/null 2>&1 || true
  fi
  rm -rf "$RUN_DIR"
  exit "$rc"
}
trap cleanup EXIT INT TERM

seed_profile() {
  local profile="$1" theme="$2" authenticated="$3"
  local chrome_pid
  mkdir -p "$profile"
  "$CHROME" \
    --headless=new \
    --disable-gpu \
    --disable-dev-shm-usage \
    --no-first-run \
    --no-default-browser-check \
    --remote-debugging-port="$DEBUG_PORT" \
    --user-data-dir="$profile" \
    --window-size=3840,2160 \
    "$BASE_URL/login" >/dev/null 2>&1 &
  chrome_pid=$!

  for _ in $(seq 1 60); do
    curl -fsS "http://127.0.0.1:$DEBUG_PORT/json/list" >/dev/null 2>&1 && break
    sleep 0.25
  done
  curl -fsS "http://127.0.0.1:$DEBUG_PORT/json/list" >/dev/null 2>&1 || fail "Chrome DevTools endpoint did not start"

  V1F_LOGIN_EMAIL="$BOOTSTRAP_ADMIN_EMAIL" \
  V1F_LOGIN_PASSWORD="$BOOTSTRAP_ADMIN_PASSWORD" \
  V1F_THEME="$theme" \
  V1F_AUTHENTICATED="$authenticated" \
  V1F_DEBUG_URL="http://127.0.0.1:$DEBUG_PORT" \
  node <<'NODE'
const debugUrl = process.env.V1F_DEBUG_URL
const pages = await (await fetch(`${debugUrl}/json/list`)).json()
const page = pages.find((item) => item.type === 'page')
if (!page?.webSocketDebuggerUrl) throw new Error('no Chrome page target')
const socket = new WebSocket(page.webSocketDebuggerUrl)
await new Promise((resolve, reject) => {
  socket.addEventListener('open', resolve, { once: true })
  socket.addEventListener('error', reject, { once: true })
})
let nextId = 1
const pending = new Map()
socket.addEventListener('message', (event) => {
  const message = JSON.parse(event.data)
  if (!message.id || !pending.has(message.id)) return
  const { resolve, reject } = pending.get(message.id)
  pending.delete(message.id)
  if (message.error) reject(new Error(message.error.message))
  else resolve(message.result)
})
function call(method, params = {}) {
  const id = nextId++
  socket.send(JSON.stringify({ id, method, params }))
  return new Promise((resolve, reject) => pending.set(id, { resolve, reject }))
}
await call('Runtime.enable')
await call('Page.enable')
const authenticated = process.env.V1F_AUTHENTICATED === '1'
const expression = `(async () => {
  const theme = ${JSON.stringify(process.env.V1F_THEME || 'light')}
  if (${authenticated ? 'true' : 'false'}) {
    const response = await fetch('/api/v1/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        email: ${JSON.stringify(process.env.V1F_LOGIN_EMAIL || '')},
        password: ${JSON.stringify(process.env.V1F_LOGIN_PASSWORD || '')}
      })
    })
    const session = await response.json()
    if (!response.ok || !session.token) throw new Error('dashboard login failed with status ' + response.status)
    localStorage.setItem('neilico.remember', 'true')
    localStorage.setItem('neilico.access_token', session.token)
    localStorage.setItem('neilico.refresh_token', session.refresh_token)
    localStorage.setItem('neilico.user', JSON.stringify(session.user))
  }
  localStorage.setItem('neilico.theme', theme)
  return { ok: true }
})()`
const result = await call('Runtime.evaluate', {
  expression,
  awaitPromise: true,
  returnByValue: true
})
if (result.exceptionDetails || !result.result?.value?.ok) throw new Error('failed to seed Chrome profile')
await call('Page.reload', { ignoreCache: true })
await new Promise((resolve) => setTimeout(resolve, 500))
try { await call('Browser.close') } catch {}
socket.close()
NODE

  for _ in $(seq 1 60); do
    kill -0 "$chrome_pid" 2>/dev/null || break
    sleep 0.1
  done
  kill "$chrome_pid" 2>/dev/null || true
  wait "$chrome_pid" 2>/dev/null || true
}

capture() {
  local output="$1" url="$2" profile="$3"
  "$CHROME" \
    --headless=new \
    --disable-gpu \
    --disable-dev-shm-usage \
    --no-first-run \
    --no-default-browser-check \
    --hide-scrollbars \
    --user-data-dir="$profile" \
    --window-size=3840,2160 \
    --screenshot="$output" \
    --virtual-time-budget=8000 \
    "$url" >/dev/null 2>&1
  [[ -s "$output" ]] || fail "screenshot is missing or empty: $output"
}

require_command docker
require_command curl
require_command openssl
require_command node
require_command python3
[[ -x "$CHROME" ]] || fail "system Chrome not found at $CHROME"

if docker ps -a --filter "label=com.docker.compose.project=$PROJECT" --format '{{.Names}}' | grep -q .; then
  log "==> removing stale containers from prior $PROJECT run"
  "${COMPOSE[@]}" --profile disabled down -v --remove-orphans >/dev/null 2>&1 || true
fi

for port in "$CONTROL_HTTP_PORT" "$CONTROL_HTTPS_PORT" "$PROXY_HTTP_PORT" "$PROXY_HTTPS_PORT" \
  "$DASHBOARD_PORT" "$POSTGRES_PORT" "$REDIS_PORT" "$NATS_PORT" "$DEBUG_PORT"; do
  assert_port_free "$port"
done

mkdir -p "$OUT_DIR"
find "$OUT_DIR" -maxdepth 1 -type f -name '*.png' -delete
openssl req -x509 -newkey rsa:2048 -nodes -days 2 \
  -keyout "$RUN_DIR/upstream.key" -out "$RUN_DIR/upstream.crt" \
  -subj "/CN=neilico-v1f-upstream" >/dev/null 2>&1
chmod 600 "$RUN_DIR/upstream.key"

cat > "$RUN_DIR/dashboard-nginx.conf" <<'NGINX'
server {
    listen 8080;
    server_name _;
    root /usr/share/nginx/html;
    index index.html;
    location / { try_files $uri $uri/ /index.html; }
    location /api/ {
        proxy_pass https://control-api:8080;
        proxy_ssl_verify off;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
    location = /healthz { proxy_pass https://control-api:8080/healthz; proxy_ssl_verify off; }
    location = /metrics { proxy_pass https://control-api:8080/metrics; proxy_ssl_verify off; }
}
NGINX

cat > "$RUN_DIR/docker-compose.v1f.yml" <<YAML
services:
  control-api:
    environment:
      NEILICO_SERVER_TLS_CLIENT_AUTH: "request"
      NEILICO_PKI_ENABLED: "true"
      NEILICO_PKI_CA_COMMON_NAME: "NEILICO V1-F Visual QA CA"
      NEILICO_PKI_SERVER_HOSTS: "localhost,127.0.0.1,control-api"
  dashboard:
    volumes:
      - "$RUN_DIR/dashboard-nginx.conf:/etc/nginx/conf.d/default.conf:ro"
YAML

COMPOSE+=(-f "$RUN_DIR/docker-compose.v1f.yml")
log "==> starting isolated NEILICO V1-F stack (project=$PROJECT)"
if ! "${COMPOSE[@]}" build dashboard control-api >"$RUN_DIR/compose-build.log" 2>&1; then
  cat "$RUN_DIR/compose-build.log" >&2
  fail "isolated stack image build failed"
fi
if ! "${COMPOSE[@]}" up -d postgres redis nats control-api dashboard >"$RUN_DIR/compose-up.log" 2>&1; then
  cat "$RUN_DIR/compose-up.log" >&2
  fail "isolated stack startup failed"
fi

for _ in $(seq 1 120); do
  if curl -fsS "http://127.0.0.1:$CONTROL_HTTP_PORT/healthz" >/dev/null 2>&1 \
    && curl -fsS "$BASE_URL/login" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl -fsS "http://127.0.0.1:$CONTROL_HTTP_PORT/healthz" >/dev/null || fail "control-plane did not become healthy"
curl -fsS "$BASE_URL/login" >/dev/null || fail "dashboard did not become healthy"

log "==> preparing clean day/night and authenticated Chrome profiles"
seed_profile "$PROFILE_BASE-light" light 0
seed_profile "$PROFILE_BASE-dark" dark 0
seed_profile "$PROFILE_BASE-auth-light" light 1
seed_profile "$PROFILE_BASE-auth-dark" dark 1

pages=(
  "01-login|/login|0"
  "02-dashboard|/|1"
  "03-nodes|/nodes|1"
  "04-certificates|/certificates|1"
  "05-networks|/networks|1"
  "06-alerts|/alerts|1"
  "07-logs|/logs|1"
  "08-settings|/settings|1"
  "09-api-tokens|/tokens|1"
)

for entry in "${pages[@]}"; do
  IFS='|' read -r name route authenticated <<<"$entry"
  for theme in light dark; do
    suffix="$theme"
    profile="$PROFILE_BASE-$suffix"
    if [[ "$authenticated" == "1" ]]; then
      suffix="auth-$theme"
      profile="$PROFILE_BASE-$suffix"
    fi
    output="$OUT_DIR/${name}-${theme}.png"
    capture "$output" "$BASE_URL$route" "$profile"
    log "captured $output"
  done
done

log ""
log "==> screenshot inventory"
python3 - "$OUT_DIR" <<'PY'
from pathlib import Path
from PIL import Image
import sys

root = Path(sys.argv[1])
files = sorted(root.glob('*.png'))
if len(files) != 18:
    raise SystemExit(f'expected 18 screenshots, found {len(files)}')
for path in files:
    with Image.open(path) as image:
        print(f"{path}\t{path.stat().st_size} bytes\t{image.width}x{image.height}")
PY

log "screenshots rc=0"
