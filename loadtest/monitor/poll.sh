#!/usr/bin/env bash
# Polls resource usage every INTERVAL_SEC seconds until stopped (Ctrl-C),
# appending one timestamped CSV row per tick. See loadtest/README.md.
#
# Usage: monitor/poll.sh <output.csv> [server_pid]
#   INTERVAL_SEC, DB_CONTAINER, PISTON_CONTAINER, RUNNER_CONTAINER env-overridable.
set -uo pipefail
cd "$(git rev-parse --show-toplevel)"

OUT_CSV="${1:?usage: poll.sh <output.csv> [server_pid]}"
SERVER_PID="${2:-}"
INTERVAL_SEC="${INTERVAL_SEC:-5}"

DB_CONTAINER="${DB_CONTAINER:-exam_taker_db}"
PISTON_CONTAINER="${PISTON_CONTAINER:-exam_taker_piston}"
RUNNER_CONTAINER="${RUNNER_CONTAINER:-exam_taker_runner}"

mkdir -p "$(dirname "$OUT_CSV")"
if [ ! -s "$OUT_CSV" ]; then
  echo "timestamp_utc,db_cpu_pct,db_mem_mb,piston_cpu_pct,piston_mem_mb,runner_cpu_pct,runner_mem_mb,pg_connections,api_cpu_pct,api_rss_mb" > "$OUT_CSV"
fi

mib_to_mb() {
  # "123MiB" / "1.2GiB" / "456KiB" -> plain MB number, "NA" if unparseable
  local raw="$1"
  case "$raw" in
    *GiB) echo "${raw%GiB} * 1024" | bc ;;
    *MiB) echo "${raw%MiB}" ;;
    *KiB) echo "${raw%KiB} / 1024" | bc ;;
    *) echo "NA" ;;
  esac
}

docker_stat() {
  # prints "cpu_pct,mem_mb" for container $1, "NA,NA" if not running
  local name="$1" line cpu mem
  line=$(docker stats --no-stream --format '{{.CPUPerc}},{{.MemUsage}}' "$name" 2>/dev/null) || { echo "NA,NA"; return; }
  [ -z "$line" ] && { echo "NA,NA"; return; }
  cpu=$(echo "$line" | cut -d, -f1 | tr -d '%')
  mem=$(echo "$line" | cut -d, -f2 | cut -d/ -f1 | tr -d ' ')
  echo "${cpu:-NA},$(mib_to_mb "$mem")"
}

pg_connections() {
  docker exec "$DB_CONTAINER" psql -U exam -d exam_taker -tAc \
    "select count(*) from pg_stat_activity where datname='exam_taker'" 2>/dev/null | tr -d ' ' \
    || echo NA
}

api_proc_stats() {
  # prints "cpu_pct,rss_mb" for pid $1, "NA,NA" if not given/alive
  local pid="$1"
  if [ -z "$pid" ] || ! kill -0 "$pid" 2>/dev/null; then echo "NA,NA"; return; fi
  ps -o %cpu=,rss= -p "$pid" 2>/dev/null | awk '{printf "%s,%.1f", $1, $2/1024}'
}

trap 'echo "poll.sh: stopped"; exit 0' INT TERM

echo "poll.sh: polling every ${INTERVAL_SEC}s -> $OUT_CSV (Ctrl-C to stop)"
while true; do
  ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  IFS=, read -r db_cpu db_mem <<< "$(docker_stat "$DB_CONTAINER")"
  IFS=, read -r piston_cpu piston_mem <<< "$(docker_stat "$PISTON_CONTAINER")"
  IFS=, read -r runner_cpu runner_mem <<< "$(docker_stat "$RUNNER_CONTAINER")"
  pgconn=$(pg_connections)
  IFS=, read -r api_cpu api_rss <<< "$(api_proc_stats "$SERVER_PID")"
  echo "$ts,$db_cpu,$db_mem,$piston_cpu,$piston_mem,$runner_cpu,$runner_mem,$pgconn,${api_cpu:-NA},${api_rss:-NA}" >> "$OUT_CSV"
  sleep "$INTERVAL_SEC"
done
