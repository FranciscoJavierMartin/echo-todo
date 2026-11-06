#!/usr/bin/env bash
# Logical backup of the compose Postgres (custom format, compressed).
#   ./scripts/backup.sh [outfile]
set -euo pipefail
out=${1:-backups/todo-$(date -u +%Y%m%dT%H%M%SZ).dump}
mkdir -p "$(dirname "$out")"
docker compose exec -T postgres pg_dump -U todo -d todo --format=custom --compress=6 --no-owner > "$out"
echo "backup written to $out ($(du -h "$out" | cut -f1))"
