#!/usr/bin/env bash
# Runs the full certification scenario against a deployed server with a
# specific candidate count, capturing a reproducibility manifest (git SHA,
# branch, timestamp, the exact parameters used) alongside the k6 results —
# see loadtest/README.md's Phase 12 (Level A/B certification).
#
# Usage: certify.sh <base_url> <load> <run_id> [extra k6 env as KEY=VAL ...]
# Example:
#   loadtest/certify.sh https://exam.example.com 300 cert300-20260906-01
#   loadtest/certify.sh https://exam.example.com 200 cert200-20260906-01 EXAM_DURATION_MIN=60
set -uo pipefail
cd "$(git rev-parse --show-toplevel)"

BASE_URL="${1:?usage: certify.sh <base_url> <load> <run_id> [KEY=VAL ...]}"
LOAD="${2:?usage: certify.sh <base_url> <load> <run_id> [KEY=VAL ...]}"
RUN_ID="${3:?usage: certify.sh <base_url> <load> <run_id> [KEY=VAL ...]}"
shift 3
EXTRA_ENV=("$@")

RESULTS_DIR="loadtest/results/$RUN_ID"
mkdir -p "$RESULTS_DIR"

GIT_SHA=$(git rev-parse HEAD 2>/dev/null || echo unknown)
GIT_BRANCH=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo unknown)
STARTED_AT=$(date -u +%Y-%m-%dT%H:%M:%SZ)

{
  echo "{"
  echo "  \"runId\": \"$RUN_ID\","
  echo "  \"baseUrl\": \"$BASE_URL\","
  echo "  \"load\": $LOAD,"
  echo "  \"gitSha\": \"$GIT_SHA\","
  echo "  \"gitBranch\": \"$GIT_BRANCH\","
  echo "  \"startedAtUtc\": \"$STARTED_AT\","
  echo "  \"extraEnv\": \"${EXTRA_ENV[*]:-}\""
  echo "}"
} > "$RESULTS_DIR/manifest.json"

echo "certify: run $RUN_ID — $LOAD candidates against $BASE_URL (git $GIT_SHA on $GIT_BRANCH)"
echo "certify: manifest written to $RESULTS_DIR/manifest.json"

# shellcheck disable=SC2086
env BASE_URL="$BASE_URL" VUS="$LOAD" RESULTS_DIR="$RESULTS_DIR" "${EXTRA_ENV[@]}" \
  k6 run loadtest/k6/scenarios/certification_run.js 2>&1 | tee "$RESULTS_DIR/k6.log"

echo "certify: done — results in $RESULTS_DIR (k6.log, certification_run.summary.json, certification_run.meta.json, manifest.json)"
echo "certify: next — go run ./loadtest/verify --exam-id=<id from meta.json> --check=all"
