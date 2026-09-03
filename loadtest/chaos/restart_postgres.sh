#!/usr/bin/env bash
# Restarts the Postgres container mid-test, to prove the API/candidates
# recover cleanly (Test 26). Logs UTC timestamps for correlation with
# k6/monitor output.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) chaos: restarting postgres (db)"
docker compose restart db
echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) chaos: postgres restart issued"
