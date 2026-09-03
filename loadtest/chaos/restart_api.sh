#!/usr/bin/env bash
# Kills and restarts the Go API process (native `go run ./cmd/server`, not
# containerized — see loadtest/README.md), to exercise RequeueStuck() and
# session/answer/token survival across a process restart (Tests 27-28).
# Expects the server to have been started via `make loadtest-server-start`
# (or anything else that writes its PID to the pidfile below).
set -uo pipefail
cd "$(git rev-parse --show-toplevel)"

PIDFILE="${1:-loadtest/results/.server.pid}"
LOGFILE="${2:-loadtest/results/server.log}"

if [ ! -f "$PIDFILE" ]; then
  echo "chaos: no pidfile at $PIDFILE — start the server via loadtest's launcher first" >&2
  exit 1
fi
PID=$(cat "$PIDFILE")

echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) chaos: killing API pid $PID"
kill "$PID" 2>/dev/null || echo "chaos: pid $PID already gone"
sleep 1

echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) chaos: rebuilding + restarting API"
go build -o bin/loadtest-server ./cmd/server
mkdir -p "$(dirname "$LOGFILE")"
nohup ./bin/loadtest-server >> "$LOGFILE" 2>&1 &
NEWPID=$!
echo "$NEWPID" > "$PIDFILE"
echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) chaos: API restarted, new pid $NEWPID"
