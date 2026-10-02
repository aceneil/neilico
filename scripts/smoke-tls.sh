#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE=(docker compose --project-name umpp-v1s-tls -f "$ROOT/deploy/docker-compose/docker-compose.yml" -f "$ROOT/deploy/docker-compose/docker-compose.tls.yml")
export POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-umpp-smoke-password}"
export UMPP_JWT_SECRET="${UMPP_JWT_SECRET:-umpp-smoke-jwt-secret-please-change}"
export BOOTSTRAP_ADMIN_PASSWORD="${BOOTSTRAP_ADMIN_PASSWORD:-umpp-smoke-admin-password}"
export BOOTSTRAP_ADMIN_EMAIL="${BOOTSTRAP_ADMIN_EMAIL:-admin@tls-smoke.test}"
export TLS_API_HTTP_PORT="${TLS_API_HTTP_PORT:-18080}"
export TLS_API_HTTPS_PORT="${TLS_API_HTTPS_PORT:-18443}"
export TLS_PROXY_HTTP_PORT="${TLS_PROXY_HTTP_PORT:-18081}"
export TLS_PROXY_HTTPS_PORT="${TLS_PROXY_HTTPS_PORT:-18444}"
export TLS_SMOKE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/umpp-tls-smoke.XXXXXX")"
TMP="$TLS_SMOKE_DIR"
PASS=0
FAIL=0
SUMMARY=()
CLEANED=0

log() { printf '%s\n' "$*"; }
start_step() { log ""; log "==> $*"; }
finish_step() {
  local result="$1"; shift
  if [[ "$result" == PASS ]]; then PASS=$((PASS+1)); else FAIL=$((FAIL+1)); fi
  SUMMARY+=("$result	$*")
  log "--- $result: $*"
}
fail_step() {
  finish_step FAIL "$1"
  log "FAILURE: $1"
  exit 1
}
expect_status() {
  local want="$1" got="${2:-$HTTP_CODE}"
  [[ "$got" == "$want" ]] || fail_step "HTTP status $got, want $want (body=${HTTP_BODY:0:500})"
}
json() { jq -r "$1" "$HTTP_BODY_FILE"; }
admin_call() {
  local method="$1" url="$2" body="${3:-}"
  local args=(-sS --cacert "$TMP/ca.crt" --connect-timeout 5 --max-time 20 -X "$method"
    --oauth2-bearer "$ADMIN_TOKEN" -o "$HTTP_BODY_FILE" -w '%{http_code}')
  [[ -n "${CLIENT_CERT:-}" ]] && args+=(--cert "$CLIENT_CERT" --key "$CLIENT_KEY")
  [[ -n "$body" ]] && args+=(-H 'Content-Type: application/json' --data "$body")
  args+=("$url")
  HTTP_CODE="$(curl "${args[@]}" 2>"$TMP/http.err" || true)"
  HTTP_BODY="$(cat "$HTTP_BODY_FILE" 2>/dev/null || true)"
}
http_call() {
  local method="$1" url="$2" token="${3:-}" body="${4:-}"
  local args=(-sS --connect-timeout 5 --max-time 20 -X "$method" -o "$HTTP_BODY_FILE" -w '%{http_code}')
  [[ -n "${CA_FILE:-}" ]] && args+=(--cacert "$CA_FILE")
  [[ -n "${CLIENT_CERT:-}" ]] && args+=(--cert "$CLIENT_CERT" --key "$CLIENT_KEY")
  [[ -n "$token" ]] && args+=(--oauth2-bearer "$token")
  [[ -n "$body" ]] && args+=(-H 'Content-Type: application/json' --data "$body")
  args+=("$url")
  HTTP_CODE="$(curl "${args[@]}" 2>"$TMP/http.err" || true)"
  HTTP_BODY="$(cat "$HTTP_BODY_FILE" 2>/dev/null || true)"
}
cleanup() {
  local rc=$?
  if [[ "$CLEANED" == 1 ]]; then return; fi
  CLEANED=1
  if [[ $rc -ne 0 ]]; then
    log ""
    log "=== failure diagnostics: docker compose logs (tail 50) ==="
    "${COMPOSE[@]}" logs --tail=50 || true
  fi
  "${COMPOSE[@]}" --profile tls-smoke down -v --remove-orphans >/dev/null 2>&1 || true
  python3 - "$TMP" <<'PYCLEAN'
import shutil, sys
shutil.rmtree(sys.argv[1], ignore_errors=True)
PYCLEAN
  exit "$rc"
}
trap cleanup EXIT INT TERM

command -v docker >/dev/null || fail_step "docker is required"
command -v jq >/dev/null || fail_step "jq is required"
command -v curl >/dev/null || fail_step "curl is required"
command -v openssl >/dev/null || fail_step "openssl is required"

CA_FILE=""
CLIENT_CERT=""
CLIENT_KEY=""
HTTP_BODY_FILE="$TMP/http-body"
: > "$HTTP_BODY_FILE"

start_step "1/10 compose validation and free-port check"
if docker ps --format '{{.Names}}' | grep -Eq 'umpp-v1s-tls'; then
  fail_step "an existing UMPP smoke container is present; refusing to touch it"
fi
"${COMPOSE[@]}" config -q || fail_step "docker compose config -q failed"
finish_step PASS "compose config -q"

openssl req -x509 -newkey rsa:2048 -nodes -days 2 \
  -keyout "$TMP/upstream.key" -out "$TMP/upstream.crt" \
  -subj "/CN=upstream.tls.test" \
  -addext "subjectAltName=IP:100.64.252.9,DNS:upstream.tls.test" >/dev/null 2>&1 || fail_step "generate HTTPS upstream certificate"
cat > "$TMP/nginx.conf" <<NGINX
events {}
http {
  server {
    listen 443 ssl;
    server_name upstream.tls.test;
    ssl_certificate /etc/nginx/tls/upstream.crt;
    ssl_certificate_key /etc/nginx/tls/upstream.key;
    location / { return 200 'UMPP_HTTPS_UPSTREAM_OK\n'; }
  }
}
NGINX
chmod 600 "$TMP/upstream.key"

start_step "2/10 start TLS stack and wait for HTTPS health"
"${COMPOSE[@]}" --profile tls-smoke build control-api >/dev/null || fail_step "docker compose build failed"
"${COMPOSE[@]}" --profile tls-smoke up -d >/dev/null || fail_step "docker compose up failed"
for _ in $(seq 1 60); do
  if curl -fsS --max-time 3 "http://127.0.0.1:$TLS_API_HTTP_PORT/healthz" >/dev/null 2>&1; then break; fi
  sleep 2
done
curl -fsS --max-time 5 "http://127.0.0.1:$TLS_API_HTTP_PORT/healthz" >/dev/null || fail_step "plaintext API health did not become ready"
curl -sk --max-time 5 "https://127.0.0.1:$TLS_API_HTTPS_PORT/api/v1/pki/ca" > "$TMP/ca-response.json" || fail_step "download PKI CA"
jq -r '.ca_cert_pem // empty' "$TMP/ca-response.json" > "$TMP/ca.crt" || fail_step "parse PKI CA"
[[ -s "$TMP/ca.crt" ]] || fail_step "PKI CA response was empty"
chmod 600 "$TMP/ca.crt"
CA_FILE="$TMP/ca.crt"
curl -fsS --cacert "$TMP/ca.crt" --max-time 5 "https://127.0.0.1:$TLS_API_HTTPS_PORT/healthz" >/dev/null || fail_step "HTTPS health with --cacert failed"
finish_step PASS "HTTPS /healthz trusted with generated CA"

start_step "3/10 plaintext API exemptions and redirects"
HTTP_CODE="$(curl -sS -o "$HTTP_BODY_FILE" -w '%{http_code}' "http://127.0.0.1:$TLS_API_HTTP_PORT/healthz" || true)"
expect_status 200
HTTP_CODE="$(curl -sS -o "$HTTP_BODY_FILE" -w '%{http_code}' "http://127.0.0.1:$TLS_API_HTTP_PORT/metrics" || true)"
expect_status 200
HTTP_CODE="$(curl -sS -o "$HTTP_BODY_FILE" -w '%{http_code}' "http://127.0.0.1:$TLS_API_HTTP_PORT/api/v1/nodes" || true)"
expect_status 301
HTTP_CODE="$(curl -sS -X POST -o "$HTTP_BODY_FILE" -w '%{http_code}' "http://127.0.0.1:$TLS_API_HTTP_PORT/api/v1/nodes" || true)"
expect_status 308
finish_step PASS "API plaintext /healthz,/metrics exempt; GET 301 and POST 308"

start_step "4/10 mTLS protected endpoint and probe exception"
HTTP_CODE="$(curl -sS --cacert "$TMP/ca.crt" -o "$HTTP_BODY_FILE" -w '%{http_code}' "https://127.0.0.1:$TLS_API_HTTPS_PORT/api/v1/nodes" || true)"
expect_status 401
HTTP_CODE="$(curl -sS --cacert "$TMP/ca.crt" -o "$HTTP_BODY_FILE" -w '%{http_code}' "https://127.0.0.1:$TLS_API_HTTPS_PORT/healthz" || true)"
expect_status 200
finish_step PASS "API without client certificate rejected; /healthz remains available"

start_step "5/10 bootstrap login, node enrollment, and mTLS certificate"
http_call POST "https://127.0.0.1:$TLS_API_HTTPS_PORT/api/v1/auth/login" "" "$(jq -nc --arg e "$BOOTSTRAP_ADMIN_EMAIL" --arg p "$BOOTSTRAP_ADMIN_PASSWORD" '{email:$e,password:$p}')"
expect_status 200
ADMIN_TOKEN="$(jq -r '.token // empty' "$HTTP_BODY_FILE")"
[[ -n "$ADMIN_TOKEN" && "$ADMIN_TOKEN" == *.* ]] || fail_step "login response omitted a valid access token"
REGISTER_PAYLOAD='{"name":"tls-smoke-node","os":"linux","arch":"amd64","version":"smoke","tags":["tls-smoke"]}'
HTTP_CODE="$(curl -sS --cacert "$TMP/ca.crt" --connect-timeout 5 --max-time 20 -X POST \
  --oauth2-bearer "$ADMIN_TOKEN" -H 'Content-Type: application/json' --data "$REGISTER_PAYLOAD" \
  -o "$HTTP_BODY_FILE" -w '%{http_code}' "https://127.0.0.1:$TLS_API_HTTPS_PORT/api/v1/nodes/register" 2>"$TMP/register.curl" || true)"
HTTP_BODY="$(cat "$HTTP_BODY_FILE" 2>/dev/null || true)"
expect_status 201
NODE_ID="$(jq -r '.node_id // empty' "$HTTP_BODY_FILE")"
AGENT_TOKEN="$(jq -r '.agent_token // empty' "$HTTP_BODY_FILE")"
PRIVATE_KEY="$(jq -r '.private_key // empty' "$HTTP_BODY_FILE")"
PUBLIC_KEY="$(jq -r '.public_key // empty' "$HTTP_BODY_FILE")"
[[ -n "$NODE_ID" && -n "$AGENT_TOKEN" && -n "$PRIVATE_KEY" && -n "$PUBLIC_KEY" ]] || fail_step "node registration response omitted identity"
admin_call POST "https://127.0.0.1:$TLS_API_HTTPS_PORT/api/v1/nodes/$NODE_ID/mtls" ""
expect_status 201
jq -r '.client_cert_pem // empty' "$HTTP_BODY_FILE" > "$TMP/client.crt"
jq -r '.client_key_pem // empty' "$HTTP_BODY_FILE" > "$TMP/client.key"
chmod 600 "$TMP/client.crt" "$TMP/client.key"
CLIENT_CERT="$TMP/client.crt"
CLIENT_KEY="$TMP/client.key"
[[ -s "$TMP/client.crt" && -s "$TMP/client.key" ]] || fail_step "mTLS certificate response was empty"
HTTP_CODE="$(curl -sS --cacert "$TMP/ca.crt" --cert "$TMP/client.crt" --key "$TMP/client.key" -H "Authorization: Bearer $ADMIN_TOKEN" -o "$HTTP_BODY_FILE" -w '%{http_code}' "https://127.0.0.1:$TLS_API_HTTPS_PORT/api/v1/nodes" || true)"
expect_status 200
finish_step PASS "admin login/enrollment and client-certificate management request"

start_step "6/10 real Agent dry-run registration, heartbeat, and backoff"
cat > "$TMP/state.json" <<STATE
{"node_id":"$NODE_ID","agent_token":"$AGENT_TOKEN","private_key":"$PRIVATE_KEY","public_key":"$PUBLIC_KEY","applied_version":0}
STATE
chmod 600 "$TMP/state.json"
cat > "$TMP/agent.yaml" <<AGENT
server: https://127.0.0.1:$TLS_API_HTTPS_PORT
node:
  name: tls-smoke-node
  tags: ["tls-smoke"]
mesh:
  interface: wg-smoke
  mtu: 1420
  listen_port: 51820
  cleanup_on_exit: true
proxy:
  enabled: false
metrics:
  enabled: false
tls:
  ca_file: $TMP/ca.crt
  client_cert_file: $TMP/client.crt
  client_key_file: $TMP/client.key
  server_name: 127.0.0.1
  insecure_skip_verify: false
state_path: $TMP/state.json
poll_interval: 2s
heartbeat_interval: 2s
log: {level: info}
AGENT
chmod 600 "$TMP/agent.yaml"
(cd "$ROOT/agent" && CGO_ENABLED=0 go build -o "$TMP/umpp-agent" ./cmd/agent) || fail_step "build Agent"
"$TMP/umpp-agent" --dry-run --config "$TMP/agent.yaml" --state "$TMP/state.json" >"$TMP/agent.log" 2>&1 &
AGENT_PID=$!
sleep 4
kill -0 "$AGENT_PID" 2>/dev/null || fail_step "Agent exited unexpectedly during successful TLS run"
HTTP_CODE="$(curl -sS --cacert "$TMP/ca.crt" --cert "$TMP/client.crt" --key "$TMP/client.key" -H "Authorization: Bearer $ADMIN_TOKEN" -o "$HTTP_BODY_FILE" -w '%{http_code}' "https://127.0.0.1:$TLS_API_HTTPS_PORT/api/v1/nodes/$NODE_ID" || true)"
expect_status 200
jq -e '.status == "online"' "$HTTP_BODY_FILE" >/dev/null || fail_step "Agent heartbeat did not mark node online"
finish_step PASS "Agent process alive, registration state accepted, heartbeat online"

cat > "$TMP/agent-no-ca.yaml" <<AGENT
server: https://127.0.0.1:$TLS_API_HTTPS_PORT
node:
  name: tls-smoke-node
  tags: ["tls-smoke"]
mesh:
  interface: wg-smoke
  mtu: 1420
  listen_port: 51820
  cleanup_on_exit: true
proxy:
  enabled: false
metrics:
  enabled: false
tls:
  ca_file: ""
  client_cert_file: $TMP/client.crt
  client_key_file: $TMP/client.key
  server_name: 127.0.0.1
  insecure_skip_verify: false
state_path: $TMP/state.json
poll_interval: 2s
heartbeat_interval: 2s
log: {level: info}
AGENT
"$TMP/umpp-agent" --dry-run --config "$TMP/agent-no-ca.yaml" --state "$TMP/state.json" >"$TMP/agent-no-ca.log" 2>&1 &
NO_CA_PID=$!
sleep 4
kill -0 "$NO_CA_PID" 2>/dev/null || fail_step "Agent crashed after CA removal"
grep -Eqi 'certificate|x509|tls' "$TMP/agent-no-ca.log" || fail_step "Agent CA-removal log lacked certificate error"
grep -Eqi 'retrying|failed' "$TMP/agent-no-ca.log" || fail_step "Agent CA-removal log lacked retry/backoff evidence"
kill "$AGENT_PID" "$NO_CA_PID" >/dev/null 2>&1 || true
wait "$AGENT_PID" "$NO_CA_PID" >/dev/null 2>&1 || true
finish_step PASS "Agent survives CA removal, logs certificate error, and retries"

start_step "7/10 proxy redirects and HSTS"
HTTP_CODE="$(curl -sS -o "$HTTP_BODY_FILE" -w '%{http_code}' "http://127.0.0.1:$TLS_PROXY_HTTP_PORT/api/v1/nodes" || true)"
expect_status 301
HTTP_CODE="$(curl -sS -o "$HTTP_BODY_FILE" -w '%{http_code}' "http://127.0.0.1:$TLS_PROXY_HTTP_PORT/healthz" || true)"
expect_status 200
finish_step PASS "proxy plaintext redirect and health exemption"

start_step "8/10 HTTPS upstream, CA verification, and insecure override"
openssl req -x509 -newkey rsa:2048 -nodes -days 2 -keyout "$TMP/proxy.key" -out "$TMP/proxy.crt" -subj "/CN=app.tls.test" -addext "subjectAltName=DNS:app.tls.test" >/dev/null 2>&1 || fail_step "generate proxy certificate"
admin_call POST "https://127.0.0.1:$TLS_API_HTTPS_PORT/api/v1/domains" '{"domain":"app.tls.test","status":"active"}'
expect_status 201
DOMAIN_ID="$(jq -r '.id // empty' "$HTTP_BODY_FILE")"
[[ -n "$DOMAIN_ID" ]] || fail_step "domain creation omitted id"
CERT_PAYLOAD="$(jq -n --rawfile c "$TMP/proxy.crt" --rawfile k "$TMP/proxy.key" '{issuer:"",domain:"app.tls.test",cert_pem:$c,key_pem:$k}')"
admin_call POST "https://127.0.0.1:$TLS_API_HTTPS_PORT/api/v1/certificates" "$CERT_PAYLOAD"
expect_status 201
admin_call POST "https://127.0.0.1:$TLS_API_HTTPS_PORT/api/v1/proxy-rules" "$(jq -nc --arg d "$DOMAIN_ID" '{domain_id:$d,path:"/",target_type:"internal_ip",target:"100.64.252.9:443",upstream_scheme:"https",upstream_ca_file:"/certs/upstream.crt",upstream_insecure_skip_verify:false,access_control:{ip_whitelist:[],basic_auth:false,require_jwt:false},enabled:true}')"
expect_status 201
RULE_ID="$(jq -r '.id // empty' "$HTTP_BODY_FILE")"
[[ -n "$RULE_ID" ]] || fail_step "proxy rule creation omitted id"
HTTP_CODE="$(curl -sk --resolve "app.tls.test:$TLS_PROXY_HTTPS_PORT:127.0.0.1" -o "$HTTP_BODY_FILE" -w '%{http_code}' "https://app.tls.test:$TLS_PROXY_HTTPS_PORT/" || true)"
expect_status 200
grep -q 'UMPP_HTTPS_UPSTREAM_OK' "$HTTP_BODY_FILE" || fail_step "HTTPS upstream response body mismatch"
HTTP_HEADERS="$TMP/headers"; curl -sk --resolve "app.tls.test:$TLS_PROXY_HTTPS_PORT:127.0.0.1" -D "$HTTP_HEADERS" -o /dev/null "https://app.tls.test:$TLS_PROXY_HTTPS_PORT/" || true
grep -qi '^Strict-Transport-Security: max-age=31536000' "$HTTP_HEADERS" || fail_step "HSTS header missing or incorrect"
# A rule without a trust anchor must fail closed and explain certificate verification.
admin_call PUT "https://127.0.0.1:$TLS_API_HTTPS_PORT/api/v1/proxy-rules/$RULE_ID" "$(jq -nc --arg d "$DOMAIN_ID" '{domain_id:$d,path:"/",target_type:"internal_ip",target:"100.64.252.9:443",upstream_scheme:"https",upstream_ca_file:"",upstream_insecure_skip_verify:false,access_control:{ip_whitelist:[],basic_auth:false,require_jwt:false},enabled:true}')"
expect_status 200
HTTP_CODE="$(curl -sk --resolve "app.tls.test:$TLS_PROXY_HTTPS_PORT:127.0.0.1" -o "$HTTP_BODY_FILE" -w '%{http_code}' "https://app.tls.test:$TLS_PROXY_HTTPS_PORT/" || true)"
expect_status 502
"${COMPOSE[@]}" logs --no-color control-api 2>/dev/null | grep -Eqi 'x509|certificate signed by unknown authority|failed to verify certificate' \
  || fail_step "no-CA HTTPS upstream log lacked certificate reason"
admin_call PUT "https://127.0.0.1:$TLS_API_HTTPS_PORT/api/v1/proxy-rules/$RULE_ID" "$(jq -nc --arg d "$DOMAIN_ID" '{domain_id:$d,path:"/",target_type:"internal_ip",target:"100.64.252.9:443",upstream_scheme:"https",upstream_ca_file:"",upstream_insecure_skip_verify:true,access_control:{ip_whitelist:[],basic_auth:false,require_jwt:false},enabled:true}')"
expect_status 200
HTTP_CODE="$(curl -sk --resolve "app.tls.test:$TLS_PROXY_HTTPS_PORT:127.0.0.1" -o "$HTTP_BODY_FILE" -w '%{http_code}' "https://app.tls.test:$TLS_PROXY_HTTPS_PORT/" || true)"
expect_status 200
finish_step PASS "HTTPS upstream trusted with CA, fails closed without CA, insecure override succeeds"

start_step "9/10 transport security metrics"
curl -fsS --cacert "$TMP/ca.crt" --max-time 5 "https://127.0.0.1:$TLS_API_HTTPS_PORT/metrics" > "$TMP/metrics.txt" || fail_step "fetch metrics"
grep -q '^umpp_pki_certificates_issued_total' "$TMP/metrics.txt" || fail_step "PKI issuance metric missing"
grep -q '^umpp_tls_handshakes_total' "$TMP/metrics.txt" || fail_step "TLS handshake metric missing"
finish_step PASS "PKI and TLS handshake metrics present"

start_step "10/10 summary and compose state"
"${COMPOSE[@]}" --profile tls-smoke ps
log ""
log "TLS smoke summary:"
for row in "${SUMMARY[@]}"; do log "$row"; done
log "PASS=$PASS FAIL=$FAIL"
finish_step PASS "all TLS smoke checks completed"
log "tls smoke rc=0"
