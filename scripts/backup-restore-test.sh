#!/usr/bin/env bash
# Proves backups are restorable: dump, restore into a scratch DB, compare row
# counts per table and the goose version. Run regularly (and before upgrades).
set -euo pipefail
cd "$(dirname "$0")/.."
dump=$(mktemp -t todo-backup-XXXX.dump)
trap 'rm -f "$dump"' EXIT
./scripts/backup.sh "$dump"
./scripts/restore.sh "$dump" todo_restore_check

q='SELECT string_agg(t || '"'"'='"'"' || n, '"'"','"'"' ORDER BY t) FROM (
     SELECT '"'"'users'"'"' t, count(*) n FROM users UNION ALL
     SELECT '"'"'todos'"'"', count(*) FROM todos UNION ALL
     SELECT '"'"'refresh_tokens'"'"', count(*) FROM refresh_tokens UNION ALL
     SELECT '"'"'attachments'"'"', count(*) FROM attachments UNION ALL
     SELECT '"'"'outbox'"'"', count(*) FROM outbox UNION ALL
     SELECT '"'"'goose'"'"', max(version_id) FROM goose_db_version) s'
src=$(docker compose exec -T postgres psql -U todo -d todo -Atc "$q")
dst=$(docker compose exec -T postgres psql -U todo -d todo_restore_check -Atc "$q")
echo "source:   $src"
echo "restored: $dst"
docker compose exec -T postgres psql -U todo -d postgres -qc "DROP DATABASE todo_restore_check WITH (FORCE)"
if [[ "$src" != "$dst" ]]; then echo "RESTORE MISMATCH" >&2; exit 1; fi
echo "restore verified"
