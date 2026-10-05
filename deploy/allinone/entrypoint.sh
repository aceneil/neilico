#!/usr/bin/env bash
set -Eeuo pipefail

PGDATA="${PGDATA:-/var/lib/postgresql/data}"
POSTGRES_DB="${POSTGRES_DB:-neilico}"
POSTGRES_USER="${POSTGRES_USER:-neilico}"
PGHOST=127.0.0.1
PGPORT=5432
POSTGRES_LOG="${POSTGRES_LOG:-/var/log/neilico/postgres.log}"

export PGDATA PGHOST PGPORT POSTGRES_DB POSTGRES_USER

log() {
    printf '[neilico-allinone] %s\n' "$*"
}

fail() {
    printf '[neilico-allinone] ERROR: %s\n' "$*" >&2
    exit 1
}

validate_identifier() {
    local name="$1"
    local value="$2"
    [[ "$value" =~ ^[A-Za-z_][A-Za-z0-9_$-]*$ ]] || fail "$name contains unsupported characters"
}

validate_identifier POSTGRES_USER "$POSTGRES_USER"
validate_identifier POSTGRES_DB "$POSTGRES_DB"
[[ -n "${POSTGRES_PASSWORD:-}" ]] || fail "POSTGRES_PASSWORD is required"

mkdir -p "$PGDATA" "$(dirname "$POSTGRES_LOG")"
chown -R postgres:postgres "$PGDATA" "$(dirname "$POSTGRES_LOG")"
chmod 0700 "$PGDATA"

if [[ ! -s "$PGDATA/PG_VERSION" ]]; then
    log "initializing PostgreSQL data directory"
    password_file="$(mktemp "${TMPDIR:-/tmp}/neilico-pw.XXXXXX")"
    cleanup_password_file() {
        rm -f "$password_file"
    }
    trap cleanup_password_file EXIT
    printf '%s\n' "$POSTGRES_PASSWORD" >"$password_file"
    chown postgres:postgres "$password_file"
    chmod 0600 "$password_file"
    su-exec postgres initdb \
        --username="$POSTGRES_USER" \
        --pwfile="$password_file" \
        --auth-host=scram-sha-256 \
        --auth-local=trust \
        --encoding=UTF8 \
        --locale=C \
        --data-checksums
    cleanup_password_file
    trap - EXIT
else
    log "using existing PostgreSQL data directory"
fi

log "starting PostgreSQL"
su-exec postgres postgres \
    -D "$PGDATA" \
    -c listen_addresses='127.0.0.1' \
    -c logging_collector=off \
    -c log_destination=stderr > >(tee "$POSTGRES_LOG") 2>&1 &
POSTGRES_PID=$!

ready=0
for _ in $(seq 1 120); do
    if su-exec postgres pg_isready --host="$PGHOST" --port="$PGPORT" --username="$POSTGRES_USER" --dbname=postgres >/dev/null 2>&1; then
        ready=1
        break
    fi
    if ! kill -0 "$POSTGRES_PID" 2>/dev/null; then
        tail -n 100 "$POSTGRES_LOG" >&2 || true
        fail "PostgreSQL exited during startup"
    fi
    sleep 0.5
done
if [[ "$ready" -ne 1 ]]; then
    tail -n 100 "$POSTGRES_LOG" >&2 || true
    fail "PostgreSQL did not become ready within 60 seconds"
fi

log "ensuring PostgreSQL role and database"
psql_admin="$POSTGRES_USER"
if ! PGPASSWORD="$POSTGRES_PASSWORD" su-exec postgres psql --username="$psql_admin" --dbname=postgres --set=ON_ERROR_STOP=1 -tAc 'SELECT 1' >/dev/null 2>&1; then
    psql_admin=postgres
    PGPASSWORD="$POSTGRES_PASSWORD" su-exec postgres psql --username="$psql_admin" --dbname=postgres --set=ON_ERROR_STOP=1 -tAc 'SELECT 1' >/dev/null \
        || fail "cannot connect to PostgreSQL as POSTGRES_USER or postgres"
fi

export PGPASSWORD="$POSTGRES_PASSWORD"
[[ "$POSTGRES_PASSWORD" != *$'\n'* && "$POSTGRES_PASSWORD" != *$'\r'* ]] || fail "POSTGRES_PASSWORD must not contain line breaks"
sql_literal() {
    local value="${1//\'/\'\'}"
    printf "'%s'" "$value"
}
app_user_sql="$(sql_literal "$POSTGRES_USER")"
app_password_sql="$(sql_literal "$POSTGRES_PASSWORD")"
{
    printf "SELECT format('CREATE ROLE %%I LOGIN PASSWORD %%L', %s, %s) WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = %s) \\gexec\n" \
        "$app_user_sql" "$app_password_sql" "$app_user_sql"
    printf "SELECT format('ALTER ROLE %%I LOGIN PASSWORD %%L', %s, %s) WHERE EXISTS (SELECT FROM pg_roles WHERE rolname = %s) \\gexec\n" \
        "$app_user_sql" "$app_password_sql" "$app_user_sql"
} | su-exec postgres psql --username="$psql_admin" --dbname=postgres --set=ON_ERROR_STOP=1 >/dev/null

if [[ "$(su-exec postgres psql --username="$psql_admin" --dbname=postgres --tuples-only --no-align -c \
    "SELECT 1 FROM pg_database WHERE datname = '$POSTGRES_DB'")" != "1" ]]; then
    su-exec postgres psql --username="$psql_admin" --dbname=postgres --set=ON_ERROR_STOP=1 \
        -c "CREATE DATABASE \"$POSTGRES_DB\" OWNER \"$POSTGRES_USER\""
fi
unset PGPASSWORD

APP_PID=""
SHUTTING_DOWN=0
shutdown_handler() {
    SHUTTING_DOWN=1
    trap - TERM INT
    log "received shutdown signal; stopping application and PostgreSQL"
    if [[ -n "$APP_PID" ]] && kill -0 "$APP_PID" 2>/dev/null; then
        kill -TERM "$APP_PID" 2>/dev/null || true
        for _ in $(seq 1 16); do
            kill -0 "$APP_PID" 2>/dev/null || break
            sleep 0.5
        done
    fi
    if [[ -n "$APP_PID" ]] && kill -0 "$APP_PID" 2>/dev/null; then
        kill -TERM "$APP_PID" 2>/dev/null || true
    fi
    if kill -0 "$POSTGRES_PID" 2>/dev/null; then
        su-exec postgres pg_ctl --pgdata="$PGDATA" --mode=fast --wait --timeout=60 stop >/dev/null
    fi
    wait "$APP_PID" 2>/dev/null || true
    wait "$POSTGRES_PID" 2>/dev/null || true
    log "shutdown complete"
    exit 0
}
trap shutdown_handler TERM INT

export NEILICO_DATABASE_DRIVER=postgres
export NEILICO_DATABASE_DSN="postgres://${POSTGRES_USER}@${PGHOST}:${PGPORT}/${POSTGRES_DB}?sslmode=disable"
export PGPASSWORD="$POSTGRES_PASSWORD"
export NEILICO_SERVER_DASHBOARD_DIR="${NEILICO_SERVER_DASHBOARD_DIR:-/opt/neilico/dashboard}"
export NEILICO_SERVER_DASHBOARD_SPA="${NEILICO_SERVER_DASHBOARD_SPA:-true}"
export NEILICO_SERVER_PORT="${NEILICO_SERVER_PORT:-8080}"
export NEILICO_PROXY_LISTEN="${NEILICO_PROXY_LISTEN:-:8081}"
export NEILICO_ACME_HTTP_PORT="${NEILICO_ACME_HTTP_PORT:-5002}"

# ── 控制面运行用户 ────────────────────────────────────────────────────────
# 轮换密码后需要把新值原子回写 bootstrap env（宿主挂载、mode 600）。以该文件
# 属主的身份运行控制面，才能既写回成功、又不改变宿主的 owner/mode（宿主上的
# show-admin-password.sh 仍可读）。未挂载 / 属主为 root / 无法确定属主时回落到 neilico。
CONTROL_USER="neilico"
bootstrap_env_file="${NEILICO_BOOTSTRAP_ENV_FILE:-}"
if [[ -n "$bootstrap_env_file" && -f "$bootstrap_env_file" ]]; then
    env_uid="$(stat -c %u "$bootstrap_env_file" 2>/dev/null || true)"
    if [[ -n "$env_uid" && "$env_uid" != "0" && "$env_uid" != "$(id -u neilico 2>/dev/null || echo '')" ]]; then
        env_user="$(grep -E "^[^:]*:[^:]*:${env_uid}:" /etc/passwd 2>/dev/null | head -n1 | cut -d: -f1 || true)"
        if [[ -z "$env_user" ]]; then
            adduser -S -D -H -u "$env_uid" neilico-env >/dev/null 2>&1 || true
            env_user="$(grep -E "^[^:]*:[^:]*:${env_uid}:" /etc/passwd 2>/dev/null | head -n1 | cut -d: -f1 || true)"
        fi
        if [[ -n "$env_user" ]]; then
            CONTROL_USER="$env_user"
        fi
    fi
fi

log "starting NEILICO control API and dashboard"
# 轮换密码时控制面要往 /opt/neilico/bootstrap.env 原子回写（临时文件 + rename），
# 而 rename 需要目标【目录】可写：把 /opt/neilico 交给控制面运行用户，否则会
# 报 "create temp bootstrap env file: ... permission denied"（实测踩到）。
chown "$CONTROL_USER" /opt/neilico 2>/dev/null || chmod 0775 /opt/neilico 2>/dev/null || true
su-exec "$CONTROL_USER" /usr/local/bin/neilico-control-api --config /opt/neilico/configs/config.example.yaml &
APP_PID=$!

while true; do
    if ! kill -0 "$APP_PID" 2>/dev/null; then
        set +e
        wait "$APP_PID"
        APP_STATUS=$?
        set -e
        if [[ "$SHUTTING_DOWN" -eq 0 ]]; then
            log "NEILICO control API exited with status $APP_STATUS"
            su-exec postgres pg_ctl --pgdata="$PGDATA" --mode=fast --wait --timeout=60 stop >/dev/null || true
            exit "$APP_STATUS"
        fi
    fi
    if ! kill -0 "$POSTGRES_PID" 2>/dev/null; then
        log "PostgreSQL exited unexpectedly"
        kill -TERM "$APP_PID" 2>/dev/null || true
        wait "$APP_PID" 2>/dev/null || true
        exit 1
    fi
    sleep 1
done
