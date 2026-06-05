#!/usr/bin/env bash
# version-matrix.sh — Spin disposable PG instances (14-18) on alt ports and run
# pgtuner -format json against each. Verifies valid JSON and no "column does not exist"
# or SQL query errors from the version-branched checks. Leaves oremedy-test-pg
# (and any container on :5432) untouched.
#
# Exit 0 = MATRIX PASS, Exit 1 = MATRIX FAIL.
set -euo pipefail

cd "$(dirname "$0")/.."

TMPDIR_BIN="$(mktemp -d)"
BIN="${TMPDIR_BIN}/pgtuner"
go build -o "$BIN" ./cmd/pgtuner

OUTDIR="scripts/matrix-output"
mkdir -p "$OUTDIR"

declare -A PORTS=( [14]=5514 [15]=5515 [16]=5516 [17]=5517 [18]=5518 )
fail=0

for v in 14 15 16 17 18; do
  img="postgres:${v}-alpine"
  if ! docker image inspect "$img" >/dev/null 2>&1; then
    echo ">> pulling $img"
    if ! docker pull "$img" 2>/dev/null; then
      echo "!! skip PG${v} (image unavailable)"
      continue
    fi
  fi

  port="${PORTS[$v]}"

  # Ensure no stale container is holding the port from a previous failed run.
  docker ps -q --filter "publish=${port}" | xargs -r docker stop >/dev/null 2>&1 || true

  cid=$(docker run -d --rm \
        -e POSTGRES_PASSWORD=pw \
        -e POSTGRES_USER=t \
        -e POSTGRES_DB=t \
        -p "${port}:5432" \
        "$img")

  cleanup() { docker stop "$cid" >/dev/null 2>&1 || true; }
  trap cleanup EXIT

  echo ">> PG${v} starting (container ${cid:0:12} on :${port})"

  # Wait up to 30 s for the server to accept TCP connections.
  # pg_isready may return true while the server is still initialising the
  # authentication layer; add a second phase that does an actual SQL query
  # (SELECT 1) via psql to confirm the server is truly ready.
  ready=0
  for i in $(seq 1 30); do
    if docker exec "$cid" pg_isready -U t >/dev/null 2>&1; then
      # Extra check: actually connect and run a trivial query. This avoids the
      # race where pg_isready returns 0 but the server resets the first real
      # connection (TLS negotiation not complete yet).
      if docker exec "$cid" psql -U t -d t -c "SELECT 1" >/dev/null 2>&1; then
        ready=1
        break
      fi
    fi
    sleep 1
  done

  if [ "$ready" -eq 0 ]; then
    echo "!! PG${v}: server did not become ready in 30 s"; fail=1
    cleanup; trap - EXIT
    continue
  fi

  dsn="postgres://t:pw@localhost:${port}/t"
  out="${OUTDIR}/pg${v}.json"
  err_out="${OUTDIR}/pg${v}.err"

  echo ">> PG${v} running pgtuner"
  set +e
  "$BIN" -dsn "$dsn" -format json >"$out" 2>"$err_out"
  rc=$?
  set -e

  # Exit codes 0 and 1 are both acceptable:
  #   0 = no Bad-level advice, 1 = Bad advice present (e.g. fsync=off on ephemeral PG)
  #   >=2 = tool-level error (connection failure, JSON encode error, etc.)
  if [ "$rc" -ge 2 ]; then
    echo "!! PG${v}: pgtuner failed with exit $rc"
    cat "$err_out"
    fail=1
    cleanup; trap - EXIT
    continue
  fi

  # Check the .err file for SQL-level errors (column/relation does not exist, etc.)
  # We only grep the stderr for actual query errors, NOT the JSON body.
  if grep -qiE '(column|relation|table|function|operator) .* does not exist|ERROR:' "$err_out" 2>/dev/null; then
    echo "!! PG${v}: SQL/column error detected in stderr:"
    cat "$err_out"
    fail=1
  else
    echo "ok PG${v}: pgtuner ran successfully (exit ${rc})"
  fi

  # Validate JSON and confirm zero unknown-level advice via the summary field.
  # The python check is authoritative for unknown-advice detection; the grep
  # above only catches hard SQL errors in stderr.
  if ! python3 - <<PYEOF 2>/dev/null
import json, sys
with open("${out}") as f:
    d = json.load(f)
unknown_count = d.get("summary", {}).get("unknown", 0)
if unknown_count > 0:
    print(f"  WARN: ${v} summary.unknown={unknown_count}")
    sys.exit(3)
sys.exit(0)
PYEOF
  then
    echo "!! PG${v}: invalid JSON or unknown-level advice present in summary"
    fail=1
  fi

  cleanup
  trap - EXIT
done

rm -rf "$TMPDIR_BIN"

if [ "$fail" -eq 0 ]; then
  echo "MATRIX PASS"
else
  echo "MATRIX FAIL"
  exit 1
fi
