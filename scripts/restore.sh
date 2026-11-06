#!/usr/bin/env bash
# Restore a dump into a database (created if missing). Never targets "todo"
# unless FORCE=1, so a mistyped command cannot overwrite production data.
#   ./scripts/restore.sh <dumpfile> [target_db]
set -euo pipefail
dump=${1:?usage: restore.sh <dumpfile> [target_db]}
target=${2:-todo_restore}
if [[ "$target" == "todo" && "${FORCE:-}" != "1" ]]; then
  echo "refusing to restore over 'todo' without FORCE=1" >&2; exit 1
fi
docker compose exec -T postgres psql -U todo -d postgres -v ON_ERROR_STOP=1 \
  -c "DROP DATABASE IF EXISTS \"$target\" WITH (FORCE)" -c "CREATE DATABASE \"$target\""
docker compose exec -T postgres pg_restore -U todo -d "$target" --no-owner --exit-on-error < "$dump"
echo "restored $dump into $target"
