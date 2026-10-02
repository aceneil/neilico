#!/usr/bin/env bash
set -Eeuo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT_DIR/deploy/docker-compose/docker-compose.yml"
CONFIRM="${1:-}"
if [[ "$CONFIRM" != "--yes" ]]; then
  echo "This removes the NEILICO stack and named volumes (including pgdata)." >&2
  echo "Re-run as: scripts/smoke-down.sh --yes" >&2
  exit 2
fi
docker compose -f "$COMPOSE_FILE" down -v --remove-orphans
echo "NEILICO stack and volumes removed."
