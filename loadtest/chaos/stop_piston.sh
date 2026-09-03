#!/usr/bin/env bash
# Stops Piston mid-run to prove /me/run degrades to clean 502s (not crashes)
# while the rest of the API stays healthy (Test 29). Pair with start_piston.sh.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) chaos: stopping piston"
docker compose stop piston
echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) chaos: piston stopped"
