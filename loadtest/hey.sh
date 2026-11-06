#!/usr/bin/env bash
# Fixed-rate (200 rps) and saturation runs against the list endpoint.
#   JWT_SECRET=... BASE_URL=http://localhost:8080 USER_ID=<uuid> ./loadtest/hey.sh
set -euo pipefail
BASE_URL=${BASE_URL:-http://localhost:8080}
USER_ID=${USER_ID:-00000000-0000-4000-8000-000000000001}
TOKEN=$(go run ./tools/mint -sub "$USER_ID")
URL="$BASE_URL/v1/todos?limit=20&sort=-priority,due_at&status=open"

echo "== 200 rps for 30s (50 workers x 4 qps)"
hey -z 30s -c 50 -q 4 -H "Authorization: Bearer $TOKEN" "$URL"
echo "== saturation for 15s"
hey -z 15s -c 50 -H "Authorization: Bearer $TOKEN" "$URL"
