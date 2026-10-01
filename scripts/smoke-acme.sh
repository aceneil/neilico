#!/usr/bin/env bash
# UMPP V1-R1 real ACME integration smoke using Pebble. No Let's Encrypt
# production endpoint is used. Exit status is the acceptance result.
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_DIR="$ROOT_DIR/deploy/docker-compose"
BASE_FILE="$COMPOSE_DIR/docker-compose.yml"
ACME_FILE="$COMPOSE_DIR/docker-compose.acme.yml"
ENV_FILE="$COMPOSE_DIR/.env"
PROJECT_NAME="umpp-v1r1-acme"
TMP_DIR="$(mktemp -d /tmp/umpp-acme-smoke.XXXXXX)"
HTTP_BODY_FILE="$TMP_DIR/http-body"
STEP_NAMES=()
STEP_RESULTS=()
STEP_TIMES=()
STEP_DETAILS=()
STEP_STARTED=0
CURRENT_STEP=""
HTTP_STATUS=""
HTTP_BODY=""

COMPOSE=(docker compose --project-name "$PROJECT_NAME" -f "$BASE_FILE" -f "$ACME_FILE")

cleanup() {
  local rc=$?
  set +e
  rm -rf "$TMP_DIR"
  exit "$rc"
}
trap cleanup EXIT INT TERM

env_get() {
  local key="$1" default="${2:-}"
  local value
  value="$(awk -F= -v key="$key" '$1 == key { sub(/^[^=]*=/, ""); print; exit }' "$ENV_FILE" | sed -e 's/^["'\'']//' -e 's/["'\'']$//')"
  printf '%s' "${value:-$default}"
}

start_step() {
  CURRENT_STEP="$1"
  STEP_STARTED=$(date +%s%N)
  printf '\n=== %s ===\n' "$CURRENT_STEP"
}

finish_step() {
  local result="$1" detail="${2:-}" ended duration
  ended=$(date +%s%N)
  duration=$(( (ended - STEP_STARTED) / 1000000 ))
  STEP_NAMES+=("$CURRENT_STEP")
  STEP_RESULTS+=("$result")
  STEP_TIMES+=("${duration}ms")
  STEP_DETAILS+=("$detail")
  printf '%s: %s (%sms)\n' "$result" "$CURRENT_STEP" "$duration"
  [[ -n "$detail" ]] && printf 'detail: %s\n' "$detail"
}

fail_step() {
  local detail="${1:-assertion failed}"
  finish_step FAIL "$detail"
  printf '\n--- diagnostics: docker compose ps ---\n'
  "${COMPOSE[@]}" ps || true
  printf '\n--- diagnostics: container logs (tail 50) ---\n'
  "${COMPOSE[@]}" logs --tail=50 --no-color || true
  print_summary
  exit 1
}

print_summary() {
  printf '\n=== SMOKE SUMMARY ===\n'
  printf '%-52s %-6s %-10s %s\n' STEP RESULT TIME DETAIL
  printf '%-52s %-6s %-10s %s\n' '----------------------------------------------------' '------' '----------' '------'
  local i
  for ((i=0; i<${#STEP_NAMES[@]}; i++)); do
    printf '%-52s %-6s %-10s %s\n' "${STEP_NAMES[$i]}" "${STEP_RESULTS[$i]}" "${STEP_TIMES[$i]}" "${STEP_DETAILS[$i]}"
  done
  printf '\n--- docker compose ps ---\n'
  "${COMPOSE[@]}" ps || true
}

http_call() {
  local method="$1" url="$2" token="${3:-}" data="${4:-}"
  local args=(-sS --connect-timeout 5 --max-time 30 -X "$method" "$url")
  [[ -n "$token" ]] && args+=(-H "Authorization: Bearer $token")
  if [[ -n "$data" ]]; then
    args+=(-H 'Content-Type: application/json' --data-binary "$data")
  fi
  HTTP_STATUS="$(curl "${args[@]}" -o "$HTTP_BODY_FILE" -w '%{http_code}' 2>"$TMP_DIR/curl.err" || true)"
  HTTP_BODY="$(cat "$HTTP_BODY_FILE" 2>/dev/null || true)"
  if [[ "$HTTP_STATUS" == "000" ]]; then
    HTTP_BODY="$(cat "$TMP_DIR/curl.err" 2>/dev/null || true)"
  fi
}

json() {
  jq -r "$1" "$HTTP_BODY_FILE" 2>/dev/null || true
}

expect_status() {
  local expected="$1"
  [[ "$HTTP_STATUS" == "$expected" ]] || fail_step "expected HTTP $expected, got ${HTTP_STATUS}; body=${HTTP_BODY:0,500}"
}

metric_value() {
  local metric="$1"
  curl -sS --connect-timeout 5 --max-time 10 "$API_BASE/metrics" 2>/dev/null | awk -v metric="$metric" '$1 == metric { print $2; exit }'
}

if [[ ! -f "$BASE_FILE" || ! -f "$ACME_FILE" ]]; then
  echo "missing compose files" >&2
  exit 2
fi
if [[ ! -f "$ENV_FILE" ]]; then
  cp "$COMPOSE_DIR/.env.example" "$ENV_FILE"
fi
for tool in docker curl jq openssl; do
  command -v "$tool" >/dev/null 2>&1 || { echo "missing required tool: $tool" >&2; exit 2; }
done

BOOTSTRAP_EMAIL="$(env_get BOOTSTRAP_ADMIN_EMAIL admin@example.com)"
BOOTSTRAP_PASSWORD="$(env_get BOOTSTRAP_ADMIN_PASSWORD)"
CONTROL_API_PORT="$(env_get CONTROL_API_PORT 18080)"
TLS_PORT="$(env_get PROXY_TLS_PORT 18443)"
API_BASE="http://127.0.0.1:${CONTROL_API_PORT}"
RUN_ID="$(date +%Y%m%d%H%M%S)-$$"
DOMAIN_NAME="acme-smoke-${RUN_ID}.example.com"

start_step "1/10 compose overlay config validation"
if config_output="$("${COMPOSE[@]}" config -q 2>&1)"; then
  finish_step PASS "COMPOSE_OK"
else
  fail_step "docker compose config failed: $config_output"
fi

start_step "2/10 start Pebble stack and wait for /healthz"
"${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
if up_output="$("${COMPOSE[@]}" up -d --build 2>&1)"; then
  printf '%s\n' "$up_output" | tail -n 30
else
  fail_step "docker compose up failed: $up_output"
fi
health_code=""
for _ in $(seq 1 180); do
  health_code="$(curl -sS --connect-timeout 2 --max-time 3 -o "$HTTP_BODY_FILE" -w '%{http_code}' "$API_BASE/healthz" 2>/dev/null || true)"
  [[ "$health_code" == "200" ]] && break
  sleep 1
done
[[ "$health_code" == "200" ]] || fail_step "healthz did not return 200 within 180s (last code=${health_code:-none})"
finish_step PASS "GET $API_BASE/healthz -> 200"

start_step "3/10 login and create smoke domain"
login_payload="$(jq -nc --arg email "$BOOTSTRAP_EMAIL" --arg password "$BOOTSTRAP_PASSWORD" '{email:$email,password:$password}')"
http_call POST "$API_BASE/api/v1/auth/login" "" "$login_payload"
expect_status 200
ADMIN_TOKEN="$(json '.token // empty')"
[[ -n "$ADMIN_TOKEN" ]] || fail_step "login response omitted token"
http_call POST "$API_BASE/api/v1/domains" "$ADMIN_TOKEN" "$(jq -nc --arg domain "$DOMAIN_NAME" '{domain:$domain,status:"pending"}')"
expect_status 201
finish_step PASS "domain=$DOMAIN_NAME created"

start_step "4/10 request asynchronous ACME issuance"
http_call POST "$API_BASE/api/v1/certificates" "$ADMIN_TOKEN" "$(jq -nc --arg domain "$DOMAIN_NAME" '{issuer:"acme",domain:$domain}')"
expect_status 202
CERT_ID="$(json '.id // empty')"
[[ "$CERT_ID" =~ ^[0-9a-f-]{36}$ ]] || fail_step "202 response omitted certificate id"
[[ "$(json '.status')" == "pending" ]] || fail_step "202 response status=$(json '.status'), want pending"
finish_step PASS "certificate id=$CERT_ID status=pending"

start_step "5/10 poll certificate until active"
active=0
for _ in $(seq 1 120); do
  http_call GET "$API_BASE/api/v1/certificates/$CERT_ID" "$ADMIN_TOKEN" ""
  if [[ "$(json '.status')" == "active" ]]; then active=1; break; fi
  if [[ "$(json '.status')" == "failed" ]]; then fail_step "certificate failed: $(json '.last_error')"; fi
  sleep 1
done
[[ "$active" == 1 ]] || fail_step "certificate did not become active within 120s"
EXPIRES_AT="$(json '.expires_at // empty')"
ISSUER="$(json '.issuer // empty')"
[[ "$ISSUER" =~ acme|pebble ]] || fail_step "issuer=$ISSUER does not identify acme/pebble"
if ! jq -e 'has("key_pem") | not' "$HTTP_BODY_FILE" >/dev/null; then fail_step "certificate response leaked key_pem"; fi
expires_epoch="$(date -u -d "$EXPIRES_AT" +%s 2>/dev/null || echo 0)"
now_epoch="$(date -u +%s)"
(( expires_epoch > now_epoch )) || fail_step "expires_at=$EXPIRES_AT is not in the future"
finish_step PASS "status=active issuer=$ISSUER expires_at=$EXPIRES_AT"

start_step "6/10 validate issued certificate identity and Pebble chain"
printf '%s\n' "$(json '.cert_pem')" > "$TMP_DIR/api-chain.pem"
openssl x509 -in "$TMP_DIR/api-chain.pem" -out "$TMP_DIR/api-leaf.pem"
openssl x509 -in "$TMP_DIR/api-leaf.pem" -noout -subject -ext subjectAltName > "$TMP_DIR/api-cert-info.txt"
grep -F "$DOMAIN_NAME" "$TMP_DIR/api-cert-info.txt" >/dev/null || fail_step "certificate CN/SAN does not contain $DOMAIN_NAME"
PEBBLE_MANAGEMENT_PORT="$(env_get PEBBLE_MANAGEMENT_PORT 8055)"
if ! curl -fsSk --connect-timeout 5 --max-time 10 "https://127.0.0.1:${PEBBLE_MANAGEMENT_PORT}/roots/0" -o "$TMP_DIR/pebble-root.pem"; then
  fail_step "could not fetch Pebble issuance root from management API"
fi
if ! verify_output="$(openssl verify -CAfile "$TMP_DIR/pebble-root.pem" -untrusted "$TMP_DIR/api-chain.pem" "$TMP_DIR/api-leaf.pem" 2>&1)"; then
  fail_step "Pebble chain verification failed: $verify_output"
fi
finish_step PASS "CN/SAN match; chain verifies with Pebble issuance root"

start_step "7/10 TLS SNI handshake returns the requested certificate"
if ! openssl s_client -connect "127.0.0.1:${TLS_PORT}" -servername "$DOMAIN_NAME" -showcerts </dev/null >"$TMP_DIR/s-client.out" 2>"$TMP_DIR/s-client.err"; then
  fail_step "TLS handshake failed: $(tail -n 20 "$TMP_DIR/s-client.err")"
fi
awk '/-----BEGIN CERTIFICATE-----/{capture=1} capture{print} /-----END CERTIFICATE-----/{if(capture) exit}' "$TMP_DIR/s-client.out" > "$TMP_DIR/tls-leaf.pem"
[[ -s "$TMP_DIR/tls-leaf.pem" ]] || fail_step "TLS handshake returned no certificate"
api_fingerprint="$(openssl x509 -in "$TMP_DIR/api-leaf.pem" -outform DER | sha256sum | awk '{print $1}')"
tls_fingerprint="$(openssl x509 -in "$TMP_DIR/tls-leaf.pem" -outform DER | sha256sum | awk '{print $1}')"
[[ "$api_fingerprint" == "$tls_fingerprint" ]] || fail_step "TLS certificate fingerprint mismatch: api=$api_fingerprint tls=$tls_fingerprint"
finish_step PASS "SNI $DOMAIN_NAME matched issued certificate fingerprint"

start_step "8/10 assert ACME and expiry metrics"
expiry_metric="umpp_certificate_expiry_days{domain=\"$DOMAIN_NAME\"}"
expiry_value="$(metric_value "$expiry_metric")"
orders_value="$(metric_value 'umpp_acme_orders_total{result="success"}')"
awk -v value="$expiry_value" 'BEGIN { exit !(value > 0) }' || fail_step "$expiry_metric=$expiry_value, want > 0"
awk -v value="$orders_value" 'BEGIN { exit !(value >= 1) }' || fail_step "umpp_acme_orders_total{result=\"success\"}=$orders_value, want >= 1"
finish_step PASS "expiry_days=$expiry_value orders_success=$orders_value"

start_step "9/10 manual renewal creates a second real order"
orders_before="$orders_value"
http_call POST "$API_BASE/api/v1/certificates/$CERT_ID/renew" "$ADMIN_TOKEN" ""
expect_status 202
renewed=0
for _ in $(seq 1 120); do
  orders_now="$(metric_value 'umpp_acme_orders_total{result="success"}')"
  http_call GET "$API_BASE/api/v1/certificates/$CERT_ID" "$ADMIN_TOKEN" ""
  renew_count="$(json '.renew_count // 0')"
  if awk -v now="$orders_now" -v before="$orders_before" 'BEGIN { exit !(now >= before + 1) }' && [[ "$renew_count" =~ ^[0-9]+$ ]] && (( renew_count >= 1 )); then
    renewed=1
    break
  fi
  sleep 1
done
[[ "$renewed" == 1 ]] || fail_step "renewal did not complete: orders=$orders_now renew_count=$renew_count"
finish_step PASS "orders $orders_before -> $orders_now; renew_count=$renew_count"

start_step "10/10 final status and summary"
"${COMPOSE[@]}" ps
finish_step PASS "real Pebble HTTP-01 issuance, TLS SNI and renewal verified"

print_summary
printf '\nACME_SMOKE_PASS\n'
