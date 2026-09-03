#!/usr/bin/env bash
# Waits DELAY_SEC seconds, then fires one chaos script, logging UTC
# timestamps to loadtest/results/chaos.log so a chaos event can be
# correlated with the k6 and monitor timelines of the run it targeted.
# Start this at (approximately) the same moment as the k6 run it's aimed at.
#
# Usage: orchestrate.sh <delay_sec> <chaos_script.sh> [chaos_script_args...]
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

DELAY_SEC="${1:?usage: orchestrate.sh <delay_sec> <chaos_script.sh> [args...]}"
SCRIPT="${2:?usage: orchestrate.sh <delay_sec> <chaos_script.sh> [args...]}"
shift 2

LOG="${CHAOS_LOG:-loadtest/results/chaos.log}"
mkdir -p "$(dirname "$LOG")"

echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) orchestrate: waiting ${DELAY_SEC}s before $SCRIPT $*" | tee -a "$LOG"
sleep "$DELAY_SEC"
echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) orchestrate: firing $SCRIPT $*" | tee -a "$LOG"
bash "loadtest/chaos/$SCRIPT" "$@" 2>&1 | tee -a "$LOG"
echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) orchestrate: done" | tee -a "$LOG"
