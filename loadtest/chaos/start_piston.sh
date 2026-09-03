#!/usr/bin/env bash
# Restarts Piston after stop_piston.sh, then verifies subsequent /me/run
# calls recover (Test 29).
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) chaos: starting piston"
docker compose start piston
echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) chaos: piston started"
