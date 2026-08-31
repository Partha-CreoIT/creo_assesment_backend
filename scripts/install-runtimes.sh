#!/usr/bin/env bash
# Installs the Python, GCC (C) and Java runtimes into the local Piston container.
# Run once after `docker compose up -d piston`. Needs internet access.
set -uo pipefail

PISTON="${PISTON_URL:-http://localhost:2000}"

echo "Waiting for Piston at $PISTON ..."
for i in $(seq 1 60); do
  if curl -sf "$PISTON/api/v2/runtimes" >/dev/null 2>&1; then break; fi
  sleep 2
done

latest_version() {
  curl -s "$PISTON/api/v2/packages" | python3 -c "
import sys, json
pkgs = json.load(sys.stdin)
vs = [p['language_version'] for p in pkgs if p['language'] == '$1']
if not vs:
    sys.exit(1)
vs.sort(key=lambda v: [int(x) for x in v.split('.')])
print(vs[-1])
"
}

for lang in python gcc java; do
  installed=$(curl -s "$PISTON/api/v2/runtimes" | python3 -c "
import sys, json
rts = json.load(sys.stdin)
print(any(r['language'] == '$lang' or '$lang' in r.get('aliases', []) or r.get('runtime') == '$lang' for r in rts))
")
  if [ "$installed" = "True" ]; then
    echo "$lang: already installed, skipping"
    continue
  fi
  ver=$(latest_version "$lang")
  if [ -z "${ver:-}" ]; then
    echo "$lang: no package found in index" >&2
    exit 1
  fi
  echo "Installing $lang $ver (this can take a few minutes)..."
  curl -s -X POST "$PISTON/api/v2/packages" \
    -H 'Content-Type: application/json' \
    -d "{\"language\":\"$lang\",\"version\":\"$ver\"}"
  echo
done

echo "Installed runtimes:"
curl -s "$PISTON/api/v2/runtimes" | python3 -c "
import sys, json
for r in json.load(sys.stdin):
    print(f\"  {r['language']} {r['version']}\")
"
