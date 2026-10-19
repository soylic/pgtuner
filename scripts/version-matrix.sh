#!/usr/bin/env bash
# version-matrix.sh: start disposable PG 14-18 instances on alternate ports and run
# pgtuner -format json on each. Checks for valid JSON and for no "column does not exist"
# or other SQL errors from the version-specific checks. Leaves oremedy-test-pg
# (and any container on :5432) untouched.
#
# Exit 0 = MATRIX PASS, exit 1 = MATRIX FAIL.
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
    echo ">> pulling image $img"
    if ! docker pull "$img" 2>/dev/null; then
      echo "!! PG${v} skipped: the image is not available"
      continue
    fi
  fi

  port="${PORTS[$v]}"

  # A failed earlier run can leave a container on the port.
  docker ps -q --filter "publish=${port}" | xargs -r docker stop >/dev/null 2>&1 || true

  cid=$(docker run -d --rm \
        -e POSTGRES_PASSWORD=pw \
        -e POSTGRES_USER=t \
        -e POSTGRES_DB=t \
        -p "${port}:5432" \
        "$img")

  cleanup() { docker stop "$cid" >/dev/null 2>&1 || true; }
  trap cleanup EXIT

  echo ">> PG${v} starting (container ${cid:0:12} on port ${port})"

  # Wait up to 30 s. pg_isready can succeed before authentication is ready, so also run SELECT 1.
  ready=0
  for i in $(seq 1 30); do
    if docker exec "$cid" pg_isready -U t >/dev/null 2>&1; then
      # Without this query, the server can reset the first real connection.
      if docker exec "$cid" psql -U t -d t -c "SELECT 1" >/dev/null 2>&1; then
        ready=1
        break
      fi
    fi
    sleep 1
  done

  if [ "$ready" -eq 0 ]; then
    echo "!! PG${v}: the server was not ready after 30 s"; fail=1
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

  # Exit codes 0 and 1 are both acceptable.
  # 1 means Bad advice is present (for example fsync=off). 2 or more is a tool error.
  if [ "$rc" -ge 2 ]; then
    echo "!! PG${v}: pgtuner exited with code $rc"
    cat "$err_out"
    fail=1
    cleanup; trap - EXIT
    continue
  fi

  # Grep only stderr for query errors, not the JSON body.
  if grep -qiE '(column|relation|table|function|operator) .* does not exist|ERROR:' "$err_out" 2>/dev/null; then
    echo "!! PG${v}: stderr shows an SQL or column error:"
    cat "$err_out"
    fail=1
  else
    echo "ok PG${v}: pgtuner ran (exit code ${rc})"
  fi

  # The Python check is the authority for Unknown advice. The grep above catches only SQL errors.
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
    echo "!! PG${v}: the JSON is not valid, or the summary shows Unknown advice"
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
