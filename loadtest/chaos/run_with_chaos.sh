#!/usr/bin/env bash
# Runs a k6 scenario in the background and fires ONE chaos action partway
# through it (via orchestrate.sh), then waits for k6 to finish. This is what
# actually exercises the chaos/*.sh scripts against a live, in-flight
# workload rather than testing them standalone.
#
# Usage:
#   VUS=10 DURATION=60s chaos/run_with_chaos.sh <scenario.js> <delay_sec> <chaos_script.sh> [chaos_args...]
#
# Any k6-relevant env vars (VUS, DURATION, INCLUDE_RUN_CODE, ...) must be
# exported by the caller before invoking this script — they're inherited by
# the backgrounded k6 process.
set -uo pipefail
cd "$(git rev-parse --show-toplevel)"

SCENARIO="${1:?usage: run_with_chaos.sh <scenario.js> <delay_sec> <chaos_script.sh> [chaos_args...]}"
DELAY_SEC="${2:?usage: run_with_chaos.sh <scenario.js> <delay_sec> <chaos_script.sh> [chaos_args...]}"
CHAOS_SCRIPT="${3:?usage: run_with_chaos.sh <scenario.js> <delay_sec> <chaos_script.sh> [chaos_args...]}"
shift 3

RESULTS_DIR="${RESULTS_DIR:-loadtest/results/latest}"
mkdir -p "$RESULTS_DIR"

echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) run_with_chaos: starting k6 $SCENARIO (log: $RESULTS_DIR/k6_chaos.log)"
RESULTS_DIR="$RESULTS_DIR" k6 run "loadtest/k6/scenarios/$SCENARIO" > "$RESULTS_DIR/k6_chaos.log" 2>&1 &
K6_PID=$!

bash loadtest/chaos/orchestrate.sh "$DELAY_SEC" "$CHAOS_SCRIPT" "$@"

echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) run_with_chaos: waiting for k6 (pid $K6_PID) to finish"
wait "$K6_PID"
K6_EXIT=$?
echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) run_with_chaos: k6 finished (exit $K6_EXIT), see $RESULTS_DIR/k6_chaos.log"
exit "$K6_EXIT"
