#!/usr/bin/env bash
# UMPP M5 end-to-end smoke. The script is intentionally non-destructive to
# existing host containers and leaves the UMPP stack running after success.
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_DIR="$ROOT_DIR/deploy/docker-compose"
COMPOSE_FILE="$COMPOSE_DIR/docker-compose.yml"
ENV_FILE="$COMPOSE_DIR/.env"
TMP_DIR="$(mktemp -d /tmp/umpp-m5-smoke.XXXXXX)"
HTTP_BODY_FILE="$TMP_DIR/http-body"
AGENT_A_PID=""
AGENT_B_PID=""
ECHO_NAME="umpp-m5-smoke-echo-$$"
MUTATIONS=0
STEP_NAMES=()
STEP_RESULTS=()
STEP_TIMES=()
STEP_DETAILS=()
STEP_STARTED=0

cleanup() {
  local rc=$?
  set +e
  [[ -n "$AGENT_A_PID" ]] && kill "$AGENT_A_PID" 2>/dev/null
  [[ -n "$AGENT_B_PID" ]] && kill "$AGENT_B_PID" 2>/dev/null
  wait "$AGENT_A_PID" 2>/dev/null
  wait "$AGENT_B_PID" 2>/dev/null
  docker rm -f "$ECHO_NAME" >/dev/null 2>&1
  rm -rf "$TMP_DIR"
  exit "$rc"
}
trap cleanup EXIT INT TERM

env_get() {
  local key="$1"
  awk -F= -v key="$key" '$1 == key { sub(/^[^=]*=/, ""); print; exit }' "$ENV_FILE" | sed -e 's/^["'\'']//' -e 's/["'\'']$//'
}

start_step() {
  CURRENT_STEP="$1"
  STEP_STARTED=$(date +%s%N)
  printf '\n=== %s ===\n' "$CURRENT_STEP"
}

finish_step() {
  local result="$1" detail="${2:-}"
  local ended duration
  ended=$(date +%s%N)
  duration=$(( (ended - STEP_STARTED) / 1000000 ))
  STEP_NAMES+=("$CURRENT_STEP")
  STEP_RESULTS+=("$result")
  STEP_TIMES+=("${duration}ms")
  STEP_DETAILS+=("$detail")
  printf '%s: %s (%sms)\n' "$result" "$CURRENT_STEP" "$duration"
  if [[ -n "$detail" ]]; then
    printf 'detail: %s\n' "$detail"
  fi
}

fail_step() {
  local detail="${1:-assertion failed}"
  finish_step FAIL "$detail"
  printf '\n--- diagnostics: docker compose ps ---\n'
  docker compose -f "$COMPOSE_FILE" ps || true
  printf '\n--- diagnostics: container logs (tail 50) ---\n'
  docker compose -f "$COMPOSE_FILE" logs --tail=50 --no-color || true
  print_summary
  exit 1
}

print_summary() {
  printf '\n=== SMOKE SUMMARY ===\n'
  printf '%-48s %-6s %-10s %s\n' STEP RESULT TIME DETAIL
  printf '%-48s %-6s %-10s %s\n' '------------------------------------------------' '------' '----------' '------'
  local i
  for ((i=0; i<${#STEP_NAMES[@]}; i++)); do
    printf '%-48s %-6s %-10s %s\n' "${STEP_NAMES[$i]}" "${STEP_RESULTS[$i]}" "${STEP_TIMES[$i]}" "${STEP_DETAILS[$i]}"
  done
  printf '\n--- docker compose ps ---\n'
  docker compose -f "$COMPOSE_FILE" ps || true
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
  if [[ "$HTTP_STATUS" != "$expected" ]]; then
    fail_step "expected HTTP $expected, got ${HTTP_STATUS}; body=${HTTP_BODY:0:500}"
  fi
}

if [[ ! -f "$COMPOSE_FILE" ]]; then
  echo "missing compose file: $COMPOSE_FILE" >&2
  exit 2
fi
if [[ ! -f "$ENV_FILE" ]]; then
  cp "$COMPOSE_DIR/.env.example" "$ENV_FILE"
  echo "created $ENV_FILE from .env.example (replace placeholders before production)"
fi
for tool in docker curl jq go; do
  command -v "$tool" >/dev/null 2>&1 || { echo "missing required tool: $tool" >&2; exit 2; }
done

POSTGRES_DB="$(env_get POSTGRES_DB)"
POSTGRES_USER="$(env_get POSTGRES_USER)"
BOOTSTRAP_EMAIL="$(env_get BOOTSTRAP_ADMIN_EMAIL)"
BOOTSTRAP_PASSWORD="$(env_get BOOTSTRAP_ADMIN_PASSWORD)"
CONTROL_API_PORT="$(env_get CONTROL_API_PORT)"
PROXY_PORT="$(env_get PROXY_PORT)"
API_BASE="http://127.0.0.1:${CONTROL_API_PORT}"
PROXY_BASE="http://127.0.0.1:${PROXY_PORT}"
COMPOSE=(docker compose -f "$COMPOSE_FILE")
RUN_ID="$(date +%Y%m%d%H%M%S)-$$"
NETWORK_CIDR="100.64.250.0/24"
NODE_A_VIP="100.64.250.10"
NODE_B_VIP="100.64.250.200"
ECHO_PORT=5678
ADMIN_TOKEN=""
TENANT_TOKEN=""
NODE_A_TOKEN=""
NODE_B_TOKEN=""
NODE_A_ID=""
NODE_B_ID=""
NETWORK_ID=""
DOMAIN_NAME="smoke-${RUN_ID}.example.com"

start_step "1/12 compose config validation"
if config_output="$("${COMPOSE[@]}" config -q 2>&1)"; then
  finish_step PASS "COMPOSE_OK"
else
  fail_step "docker compose config failed: $config_output"
fi

start_step "2/12 start stack and poll /healthz"
if up_output="$("${COMPOSE[@]}" up -d --build 2>&1)"; then
  printf '%s\n' "$up_output" | tail -n 30
else
  fail_step "docker compose up failed: $up_output"
fi
health_ok=0
for _ in $(seq 1 180); do
  code="$(curl -sS --connect-timeout 2 --max-time 3 -o "$HTTP_BODY_FILE" -w '%{http_code}' "$API_BASE/healthz" 2>/dev/null || true)"
  if [[ "$code" == "200" ]]; then health_ok=1; break; fi
  sleep 1
done
if [[ "$health_ok" != 1 ]]; then
  fail_step "healthz did not return 200 within 180s (last code=${code:-none})"
fi
dashboard_state="unknown"
for _ in $(seq 1 60); do
  dashboard_state="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' umpp-m5-dashboard-1 2>/dev/null || true)"
  [[ "$dashboard_state" == "healthy" ]] && break
  sleep 1
done
[[ "$dashboard_state" == "healthy" ]] || fail_step "dashboard container is not healthy (state=$dashboard_state)"
finish_step PASS "GET $API_BASE/healthz -> 200; dashboard=$dashboard_state"

start_step "3/12 bootstrap admin login"
if [[ -z "$BOOTSTRAP_EMAIL" || -z "$BOOTSTRAP_PASSWORD" ]]; then
  fail_step "BOOTSTRAP_ADMIN_EMAIL/PASSWORD are missing from .env"
fi
login_payload="$(jq -nc --arg email "$BOOTSTRAP_EMAIL" --arg password "$BOOTSTRAP_PASSWORD" '{email:$email,password:$password}')"
http_call POST "$API_BASE/api/v1/auth/login" "" "$login_payload"
expect_status 200
ADMIN_TOKEN="$(json '.token // empty')"
[[ -n "$ADMIN_TOKEN" ]] || fail_step "login response did not contain access token"
finish_step PASS "bootstrap login accepted (token withheld)"

start_step "4/12 create tenant, tenant administrator, and login"
http_call GET "$API_BASE/api/v1/audit-logs?page=1&page_size=1" "$ADMIN_TOKEN" ""
expect_status 200
audit_before="$(json '.total // 0')"
tenant_payload="$(jq -nc --arg name "tenant-$RUN_ID" '{name:$name,plan:"pro"}')"
http_call POST "$API_BASE/api/v1/tenants" "$ADMIN_TOKEN" "$tenant_payload"
expect_status 201
MUTATIONS=$((MUTATIONS + 1))
TENANT_ID="$(json '.id // empty')"
[[ -n "$TENANT_ID" ]] || fail_step "tenant response omitted id"
tenant_password="$(openssl rand -hex 16)"
user_payload="$(jq -nc --arg tenant "$TENANT_ID" --arg email "tenant-admin-$RUN_ID@example.com" --arg password "$tenant_password" '{tenant_id:$tenant,email:$email,password:$password,role:"tenant_admin",status:"active"}')"
http_call POST "$API_BASE/api/v1/users" "$ADMIN_TOKEN" "$user_payload"
expect_status 201
MUTATIONS=$((MUTATIONS + 1))
tenant_login="$(jq -nc --arg email "tenant-admin-$RUN_ID@example.com" --arg password "$tenant_password" '{email:$email,password:$password}')"
http_call POST "$API_BASE/api/v1/auth/login" "" "$tenant_login"
expect_status 200
MUTATIONS=$((MUTATIONS + 1))
TENANT_TOKEN="$(json '.token // empty')"
[[ -n "$TENANT_TOKEN" ]] || fail_step "tenant administrator login omitted token"
finish_step PASS "tenant=$TENANT_ID, tenant administrator login accepted"

start_step "5/12 create virtual network"
network_payload="$(jq -nc --arg name "network-$RUN_ID" --arg cidr "$NETWORK_CIDR" '{name:$name,cidr:$cidr}')"
http_call POST "$API_BASE/api/v1/networks" "$TENANT_TOKEN" "$network_payload"
expect_status 201
MUTATIONS=$((MUTATIONS + 1))
NETWORK_ID="$(json '.id // empty')"
[[ -n "$NETWORK_ID" ]] || fail_step "network response omitted id"
finish_step PASS "network=$NETWORK_ID cidr=$NETWORK_CIDR"

start_step "6/12 build and run two real agents in dry-run"
agent_bin="$TMP_DIR/umpp-agent"
if ! (cd "$ROOT_DIR/agent" && CGO_ENABLED=0 go build -trimpath -o "$agent_bin" ./cmd/agent) >"$TMP_DIR/agent-build.log" 2>&1; then
  fail_step "agent build failed: $(tail -n 30 "$TMP_DIR/agent-build.log")"
fi

write_agent_config() {
  local path="$1" name="$2" state="$3"
  cat >"$path" <<YAML
server: "http://127.0.0.1:${CONTROL_API_PORT}"
token: "${TENANT_TOKEN}"
node:
  name: "${name}"
  tags: ["smoke"]
mesh:
  interface: "wg0"
  mtu: 1420
  listen_port: 51820
  public_endpoint: "127.0.0.1:51820"
  cleanup_on_exit: true
  allow_forwarding: false
proxy:
  enabled: false
metrics:
  enabled: false
  listen: "127.0.0.1:9100"
log:
  level: info
state_path: "${state}"
poll_interval: 1s
heartbeat_interval: 1s
YAML
}
write_agent_config "$TMP_DIR/agent-a.yaml" "node-a-$RUN_ID" "$TMP_DIR/node-a-state.json"
write_agent_config "$TMP_DIR/agent-b.yaml" "node-b-$RUN_ID" "$TMP_DIR/node-b-state.json"
"$agent_bin" --config "$TMP_DIR/agent-a.yaml" --dry-run --force-register >"$TMP_DIR/agent-a.log" 2>&1 &
AGENT_A_PID=$!
"$agent_bin" --config "$TMP_DIR/agent-b.yaml" --dry-run --force-register >"$TMP_DIR/agent-b.log" 2>&1 &
AGENT_B_PID=$!
for _ in $(seq 1 30); do
  [[ -s "$TMP_DIR/node-a-state.json" && -s "$TMP_DIR/node-b-state.json" ]] && break
  sleep 1
done
[[ -s "$TMP_DIR/node-a-state.json" && -s "$TMP_DIR/node-b-state.json" ]] || fail_step "agents did not write state; A=$(tail -n 20 "$TMP_DIR/agent-a.log"); B=$(tail -n 20 "$TMP_DIR/agent-b.log")"
NODE_A_ID="$(jq -r '.node_id' "$TMP_DIR/node-a-state.json")"
NODE_B_ID="$(jq -r '.node_id' "$TMP_DIR/node-b-state.json")"
NODE_A_TOKEN="$(jq -r '.agent_token' "$TMP_DIR/node-a-state.json")"
NODE_B_TOKEN="$(jq -r '.agent_token' "$TMP_DIR/node-b-state.json")"
[[ -n "$NODE_A_ID" && -n "$NODE_B_ID" && -n "$NODE_A_TOKEN" && -n "$NODE_B_TOKEN" ]] || fail_step "agent state omitted identity"
printf 'registered nodes A=%s B=%s (tokens withheld)\n' "$NODE_A_ID" "$NODE_B_ID"

http_call POST "$API_BASE/api/v1/networks/$NETWORK_ID/members" "$TENANT_TOKEN" "$(jq -nc --arg node "$NODE_A_ID" --arg ip "$NODE_A_VIP" '{node_id:$node,virtual_ip:$ip,role:"member"}')"
expect_status 201
MUTATIONS=$((MUTATIONS + 1))
http_call POST "$API_BASE/api/v1/networks/$NETWORK_ID/members" "$TENANT_TOKEN" "$(jq -nc --arg node "$NODE_B_ID" --arg ip "$NODE_B_VIP" '{node_id:$node,virtual_ip:$ip,role:"member"}')"
expect_status 201
MUTATIONS=$((MUTATIONS + 1))
for pair in "A:$NODE_A_ID" "B:$NODE_B_ID"; do
  label="${pair%%:*}" id="${pair#*:}"
  http_call GET "$API_BASE/api/v1/nodes/$id" "$TENANT_TOKEN" ""
  expect_status 200
  [[ "$(json '.status')" == "online" ]] || fail_step "node $label status=$(json '.status'), want online"
done
finish_step PASS "two real agents registered, joined, and both report online"

start_step "7/12 pull node A config and assert peer B virtual IP"
http_call GET "$API_BASE/api/v1/agent/config?node_id=$NODE_A_ID&version=0" "$NODE_A_TOKEN" ""
expect_status 200
config_version_before="$(json '.version // 0')"
jq -e --arg b "$NODE_B_ID" --arg want "$NODE_B_VIP/32" '[.network.peers[]? | select(.node_id == $b) | .allowed_ips[]?] | index($want) != null' "$HTTP_BODY_FILE" >/dev/null   || fail_step "node A peers do not contain B virtual IP $NODE_B_VIP/32; body=${HTTP_BODY:0,1000}"
finish_step PASS "peer B AllowedIPs contains $NODE_B_VIP/32; version=$config_version_before"

start_step "8/12 add subnet route and assert version increment"
http_call POST "$API_BASE/api/v1/networks/$NETWORK_ID/routes" "$TENANT_TOKEN" "$(jq -nc --arg node "$NODE_B_ID" '{node_id:$node,cidr:"192.168.1.0/24",enabled:true}')"
expect_status 201
MUTATIONS=$((MUTATIONS + 1))
http_call GET "$API_BASE/api/v1/agent/config?node_id=$NODE_A_ID&version=0" "$NODE_A_TOKEN" ""
expect_status 200
config_version_after="$(json '.version // 0')"
jq -e --arg b "$NODE_B_ID" --arg want "192.168.1.0/24" '[.network.peers[]? | select(.node_id == $b) | .allowed_ips[]?] | index($want) != null' "$HTTP_BODY_FILE" >/dev/null \
  || fail_step "node A peer B AllowedIPs missing 192.168.1.0/24; body=${HTTP_BODY:0,1000}"
[[ "$config_version_after" =~ ^[0-9]+$ && "$config_version_before" =~ ^[0-9]+$ && "$config_version_after" -gt "$config_version_before" ]] \
  || fail_step "config version did not increase: before=$config_version_before after=$config_version_after"
finish_step PASS "route advertised to peer B; version $config_version_before -> $config_version_after"

start_step "9/12 domain, node proxy rule, and builtin reverse proxy"
# The temporary echo container shares node B's smoke virtual IP on the Compose
# network, making target_type=node resolvable without changing host networking.
if docker run -d --name "$ECHO_NAME" --network umpp-m5_default --ip "$NODE_B_VIP" hashicorp/http-echo:1.0 -listen=":$ECHO_PORT" -text=UMPP_SMOKE_ECHO_OK >"$TMP_DIR/echo-start.log" 2>&1; then
  :
else
  fail_step "temporary echo container failed to start: $(cat "$TMP_DIR/echo-start.log")"
fi
http_call POST "$API_BASE/api/v1/domains" "$TENANT_TOKEN" "$(jq -nc --arg domain "$DOMAIN_NAME" '{domain:$domain,status:"active"}')"
expect_status 201
MUTATIONS=$((MUTATIONS + 1))
DOMAIN_ID="$(json '.id // empty')"
[[ -n "$DOMAIN_ID" ]] || fail_step "domain response omitted id"
proxy_payload="$(jq -nc --arg domain "$DOMAIN_ID" --arg node "$NODE_B_ID" --arg port "$ECHO_PORT" '{domain_id:$domain,path:"/",target_type:"node",target:($node + ":" + $port),access_control:{ip_whitelist:[],basic_auth:false,require_jwt:false},enabled:true}')"
http_call POST "$API_BASE/api/v1/proxy-rules" "$TENANT_TOKEN" "$proxy_payload"
expect_status 201
MUTATIONS=$((MUTATIONS + 1))
proxy_code="$(curl -sS --connect-timeout 5 --max-time 15 -H "Host: $DOMAIN_NAME" -o "$HTTP_BODY_FILE" -w '%{http_code}' "$PROXY_BASE/" 2>"$TMP_DIR/proxy.err" || true)"
proxy_body="$(cat "$HTTP_BODY_FILE" 2>/dev/null || true)"
[[ "$proxy_code" == "200" && "$proxy_body" == *UMPP_SMOKE_ECHO_OK* ]] || fail_step "builtin proxy request failed: code=$proxy_code body=${proxy_body:0:500} err=$(cat "$TMP_DIR/proxy.err" 2>/dev/null || true)"
finish_step PASS "Host: $DOMAIN_NAME -> target_type=node -> echo 200"

start_step "10/12 Prometheus metric assertion"
http_call GET "$API_BASE/metrics" "" ""
expect_status 200
metric_line="$(grep -E '^umpp_nodes_online ' "$HTTP_BODY_FILE" | tail -n 1 || true)"
metric_value="$(awk '{print $2}' <<<"$metric_line")"
[[ -n "$metric_value" ]] || fail_step "/metrics missing umpp_nodes_online; body=${HTTP_BODY:0,1000}"
awk -v value="$metric_value" 'BEGIN { exit !(value >= 2) }' || fail_step "umpp_nodes_online=$metric_value, want >=2"
finish_step PASS "umpp_nodes_online=$metric_value"

start_step "11/12 audit-log assertion"
http_call GET "$API_BASE/api/v1/audit-logs?page=1&page_size=1" "$ADMIN_TOKEN" ""
expect_status 200
audit_after="$(json '.total // 0')"
audit_delta=$((audit_after - audit_before))
expected_mutations=$((MUTATIONS))
[[ "$audit_delta" -ge "$expected_mutations" ]] || fail_step "audit delta=$audit_delta, expected >=$expected_mutations"
finish_step PASS "audit_logs delta=$audit_delta (>= $expected_mutations)"

start_step "12/12 final compose status"
"${COMPOSE[@]}" ps
finish_step PASS "smoke assertions complete"

print_summary
printf '\nSMOKE_PASS\n'
