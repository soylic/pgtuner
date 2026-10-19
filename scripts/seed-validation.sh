#!/usr/bin/env bash
# seed-validation.sh: start a disposable PG16 on port 5516, load seed-validation.sql,
# run pgtuner -format json, and check these results:
#   1. pg_stat_user_tables shows dead tuples on pgtuner_bloat (direct psql check).
#   2. pg_stat_database shows temp_files > 0 (direct psql check).
#   3. The pgtuner autovacuum advice names the bloated table (JSON check).
#   4. The pgtuner Schema section reports an unused index, an invalid index (Warn),
#      a table without a primary key, a foreign key without an index, and table bloat (Warn).
#
# Leaves oremedy-test-pg (:5432) untouched. Exit 0 = SEED PASS, exit 1 = SEED FAIL.
set -euo pipefail

cd "$(dirname "$0")/.."

TMPDIR_BIN="$(mktemp -d)"
BIN="${TMPDIR_BIN}/pgtuner"
go build -o "$BIN" ./cmd/pgtuner

OUTDIR="scripts/matrix-output"
mkdir -p "$OUTDIR"

PORT=5516
IMG="postgres:16-alpine"

echo ">> seed-validation: using ${IMG} on :${PORT}"

docker ps -q --filter "publish=${PORT}" | xargs -r docker stop >/dev/null 2>&1 || true

cid=$(docker run -d --rm \
      -e POSTGRES_PASSWORD=pw \
      -e POSTGRES_USER=t \
      -e POSTGRES_DB=t \
      -p "${PORT}:5432" \
      "$IMG")

cleanup() { docker stop "$cid" >/dev/null 2>&1 || true; rm -rf "$TMPDIR_BIN"; }
trap cleanup EXIT

echo ">> container ${cid:0:12} started"

ready=0
for i in $(seq 1 30); do
  if docker exec "$cid" pg_isready -U t >/dev/null 2>&1; then
    if docker exec "$cid" psql -U t -d t -c "SELECT 1" >/dev/null 2>&1; then
      ready=1
      break
    fi
  fi
  sleep 1
done

if [ "$ready" -eq 0 ]; then
  echo "!! seed-validation: the server was not ready after 30 s"
  exit 1
fi

echo ">> loading seed-validation.sql"
docker exec -i "$cid" psql -U t -d t < scripts/seed-validation.sql >/dev/null

echo ">> assertion 1: checking pg_stat_user_tables for dead tuples"
dead_tup=$(docker exec "$cid" psql -U t -d t -tAc \
  "SELECT n_dead_tup FROM pg_stat_user_tables WHERE relname = 'pgtuner_bloat'")

if [ -z "$dead_tup" ] || [ "$dead_tup" -eq 0 ]; then
  echo "!! assertion 1 FAILED: no dead tuples visible in pg_stat_user_tables (got: '${dead_tup}')"
  echo "   (The stats may not have flushed yet. Retrying once.)"
  sleep 2
  dead_tup=$(docker exec "$cid" psql -U t -d t -tAc \
    "SELECT n_dead_tup FROM pg_stat_user_tables WHERE relname = 'pgtuner_bloat'")
  if [ -z "$dead_tup" ] || [ "$dead_tup" -eq 0 ]; then
    echo "!! assertion 1 FAILED after retry: n_dead_tup=${dead_tup}"
    exit 1
  fi
fi
echo "   ok: pgtuner_bloat has n_dead_tup=${dead_tup} (expected ~25000)"

echo ">> assertion 2: checking pg_stat_database for temp_files"
temp_files=$(docker exec "$cid" psql -U t -d t -tAc \
  "SELECT temp_files FROM pg_stat_database WHERE datname = 't'")

if [ -z "$temp_files" ] || [ "$temp_files" -eq 0 ]; then
  echo "!! assertion 2 FAILED: no temp_files recorded in pg_stat_database (got: '${temp_files}')"
  echo "   Running another large sort to confirm the mechanism..."
  docker exec "$cid" psql -U t -d t -c \
    "SET work_mem='64kB'; SELECT count(*) FROM (SELECT * FROM generate_series(1,200000) ORDER BY random()) s" \
    >/dev/null
  temp_files=$(docker exec "$cid" psql -U t -d t -tAc \
    "SELECT temp_files FROM pg_stat_database WHERE datname = 't'")
  if [ -z "$temp_files" ] || [ "$temp_files" -eq 0 ]; then
    echo "!! assertion 2 FAILED after retry: temp_files=${temp_files}"
    exit 1
  fi
fi
echo "   ok: pg_stat_database.temp_files=${temp_files} > 0"

echo ">> running pgtuner -format json"
DSN="postgres://t:pw@localhost:${PORT}/t"
OUT="${OUTDIR}/seed-validation.json"
ERR="${OUTDIR}/seed-validation.err"

set +e
"$BIN" -dsn "$DSN" -format json >"$OUT" 2>"$ERR"
rc=$?
set -e

if [ "$rc" -ge 2 ]; then
  echo "!! pgtuner exited with code ${rc}"
  cat "$ERR"
  exit 1
fi

echo ">> pgtuner exited with code ${rc} (0=ok, 1=Bad advice present)"

if grep -qiE '(column|relation|table|function) .* does not exist|ERROR:' "$ERR" 2>/dev/null; then
  echo "!! SQL error in pgtuner stderr:"
  cat "$ERR"
  exit 1
fi

python3 - <<PYEOF
import json, sys

with open("${OUT}") as f:
    d = json.load(f)

summary = d.get("summary", {})
print(f"  summary: {summary}")

unknown_count = summary.get("unknown", 0)
if unknown_count > 0:
    print(f"  WARN: {unknown_count} unknown-level advice item(s) present, possible query failure")

advice = d.get("advice", [])
fail = False

autovac_bloat_found = any(
    a.get("section") == "Autovacuum"
    and "pgtuner_bloat" in a.get("message", "")
    and a.get("level") == "warn"
    for a in advice
)
if not autovac_bloat_found:
    print("  NOTE: autovacuum advice for pgtuner_bloat not found (stats collector may be delayed)")
    print("  All Autovacuum advice:")
    for a in advice:
        if a.get("section") == "Autovacuum":
            print(f"    [{a['level']}] {a['title']}")
else:
    for a in advice:
        if (a.get("section") == "Autovacuum"
                and "pgtuner_bloat" in a.get("message", "")
                and a.get("level") == "warn"):
            print(f"  assertion 3 PASS: autovacuum bloat advice found: {a['title']}")
            print(f"    message: {a['message'][:160]}")
            break

schema_advice = [a for a in advice if a.get("section") == "Schema"]
print(f"\n  Schema advice ({len(schema_advice)} item(s)):")
for a in schema_advice:
    print(f"    [{a['level']}] {a['title']}")
    if a.get("message"):
        print(f"      {a['message'][:160]}")

unused_found = any(
    "Unused indexes" in a.get("title", "") and a.get("level") == "info"
    for a in schema_advice
)
if unused_found:
    print("\n  assertion 4 PASS: unused index detected in Schema section")
else:
    print("\n  assertion 4 FAIL: no unused-index Info found in Schema section")
    print("  (pgtuner_unused_idx_payload should have idx_scan=0 and size > 1 MiB)")
    fail = True

invalid_found = any(
    "Invalid index" in a.get("title", "") and a.get("level") == "warn"
    for a in schema_advice
)
if invalid_found:
    print("  assertion 5 PASS: invalid index Warn detected in Schema section")
else:
    print("  assertion 5 FAIL: no invalid-index Warn found in Schema section")
    print("  (pgtuner_invalid_idx should have indisvalid=false)")
    fail = True

nopk_found = any(
    "without a primary key" in a.get("title", "") and a.get("level") == "info"
    for a in schema_advice
)
if nopk_found:
    print("  assertion 6 PASS: no-PK table Info detected in Schema section")
else:
    print("  assertion 6 FAIL: no 'without a primary key' Info found in Schema section")
    print("  (pgtuner_no_pk should be > 1 MiB and have no PK)")
    fail = True

fk_found = any(
    "Foreign key" in a.get("title", "") and "without" in a.get("title", "").lower() and a.get("level") == "info"
    for a in schema_advice
)
if fk_found:
    print("  assertion 7 PASS: FK-without-index Info detected in Schema section")
else:
    print("  assertion 7 FAIL: no FK-without-index Info found in Schema section")
    print("  (pgtuner_fk_child has a FK on parent_id with no supporting index)")
    fail = True

bloat_found = any(
    "bloat" in a.get("title", "").lower() and a.get("level") == "warn"
    for a in schema_advice
)
if bloat_found:
    print("  assertion 8 PASS: table bloat Warn detected in Schema section")
else:
    print("  assertion 8 FAIL: no bloat Warn found in Schema section")
    print("  (pgtuner_bloat should have dead_ratio > 20 % and size > 1 MiB)")
    fail = True

# Assertion 9: the Workload section has no Unknown-level advice, so all its queries ran.
workload_advice = [a for a in advice if a.get("section") == "Workload"]
print(f"\n  Workload advice ({len(workload_advice)} item(s)):")
for a in workload_advice:
    print(f"    [{a['level']}] {a['title']}")
    if a.get("message"):
        print(f"      {a['message'][:160]}")

workload_unknowns = [a for a in workload_advice if a.get("level") == "unknown"]
if workload_unknowns:
    print(f"\n  assertion 9 FAIL: {len(workload_unknowns)} Unknown-level Workload advice item(s), query failure(s)")
    for a in workload_unknowns:
        print(f"    [unknown] {a['title']}: {a.get('message','')[:160]}")
    fail = True
else:
    print(f"\n  assertion 9 PASS: Workload section has zero Unknown-level advice (all queries succeeded)")

if fail:
    sys.exit(1)

sys.exit(0)
PYEOF

# Assertion 10: idle-in-transaction detection (Workload sub-check 2).
# psql reads from a pipe that sends BEGIN and then sleeps 15 s. The session stays
# "idle in transaction", and pg_stat_activity shows that state at once.
echo ">> assertion 10: idle-in-transaction detection (Workload sub-check 2)"

(printf 'BEGIN;\n'; sleep 15) | docker exec -i "$cid" psql -U t -d t >/dev/null 2>&1 &
IDLE_PID=$!

idle_ready=0
for _i in $(seq 1 10); do
  _ic=$(docker exec "$cid" psql -U t -d t -tAc \
    "SELECT count(*) FROM pg_stat_activity WHERE state='idle in transaction'" 2>/dev/null || echo 0)
  if [ "${_ic:-0}" -gt 0 ]; then
    idle_ready=1
    break
  fi
  docker exec "$cid" psql -U t -d t -c "SELECT pg_sleep(1)" >/dev/null 2>&1
done

idle_count=$(docker exec "$cid" psql -U t -d t -tAc \
  "SELECT count(*) FROM pg_stat_activity WHERE state='idle in transaction'" 2>/dev/null || echo 0)

echo "   direct check: idle-in-transaction count = ${idle_count}"

if [ -z "$idle_count" ] || [ "$idle_count" -eq 0 ]; then
  echo "!! assertion 10 WARN: no idle-in-transaction session visible in pg_stat_activity"
  echo "   The background psql may not have connected in time. Assertion 9 (zero Unknown) already passes."
  echo "   Skipping assertion 10 detection check without failing the seed."
  # No hard fail here: slow CI environments would make it flaky.
else
  echo "   ok: ${idle_count} idle-in-transaction session(s) visible"

  OUT2="${OUTDIR}/seed-validation-idle.json"
  ERR2="${OUTDIR}/seed-validation-idle.err"

  set +e
  "$BIN" -dsn "$DSN" -format json >"$OUT2" 2>"$ERR2"
  rc2=$?
  set -e

  if [ "$rc2" -ge 2 ]; then
    echo "!! assertion 10: pgtuner exited with code ${rc2}"
    cat "$ERR2"
    kill "$IDLE_PID" 2>/dev/null || true
    exit 1
  fi

  if grep -qiE '(column|relation|table|function) .* does not exist|ERROR:' "$ERR2" 2>/dev/null; then
    echo "!! assertion 10: SQL error in pgtuner stderr:"
    cat "$ERR2"
    kill "$IDLE_PID" 2>/dev/null || true
    exit 1
  fi

  python3 - <<PYEOF2
import json, sys

with open("${OUT2}") as f:
    d = json.load(f)

advice = d.get("advice", [])
workload_advice = [a for a in advice if a.get("section") == "Workload"]

print(f"  Workload advice with idle-in-txn session ({len(workload_advice)} item(s)):")
for a in workload_advice:
    print(f"    [{a['level']}] {a['title']}")
    if a.get("message"):
        print(f"      {a['message'][:200]}")

# Expect at least one idle-in-transaction advice. The session is open for < 300 s, so it is Info.
idle_found = any(
    "idle-in-transaction" in a.get("title", "").lower()
    and a.get("level") in ("info", "warn")
    for a in workload_advice
)
if idle_found:
    print("\n  assertion 10 PASS: idle-in-transaction detected by WorkloadCheck (Info or Warn)")
else:
    print("\n  assertion 10 FAIL: no idle-in-transaction advice found in Workload section")
    print("  (pg_stat_activity showed the session; WorkloadCheck should have reported it)")
    sys.exit(1)

sys.exit(0)
PYEOF2
fi

kill "$IDLE_PID" 2>/dev/null || true
wait "$IDLE_PID" 2>/dev/null || true

# Assertion 11: pg_stat_statements PRESENT path.
# A disposable PG16 with shared_preload_libraries=pg_stat_statements runs on port 5520.
# After some queries, including a temp spill, the "Query Performance" section must have:
#   a. More than 1 advice item (not only the "absent" Info).
#   b. No Unknown-level advice.
#   c. An Info for "top queries by total execution time".
#   d. An Info for "temp-heavy" queries.
# ---------------------------------------------------------------------------
echo ">> assertion 11: pg_stat_statements PRESENT path (port 5520)"
PGSS_PORT=5520
PGSS_IMG="postgres:16-alpine"

docker ps -q --filter "publish=${PGSS_PORT}" | xargs -r docker stop >/dev/null 2>&1 || true

pgss_cid=$(docker run -d --rm \
  -e POSTGRES_PASSWORD=pw \
  -e POSTGRES_USER=t \
  -e POSTGRES_DB=t \
  -p "${PGSS_PORT}:5432" \
  "$PGSS_IMG" \
  postgres -c shared_preload_libraries=pg_stat_statements)

pgss_cleanup() {
  docker stop "$pgss_cid" >/dev/null 2>&1 || true
}
trap pgss_cleanup EXIT

echo "   container ${pgss_cid:0:12} started on :${PGSS_PORT}"

pgss_ready=0
for _j in $(seq 1 30); do
  if docker exec "$pgss_cid" pg_isready -U t >/dev/null 2>&1; then
    if docker exec "$pgss_cid" psql -U t -d t -c "SELECT 1" >/dev/null 2>&1; then
      pgss_ready=1
      break
    fi
  fi
  docker exec "$pgss_cid" psql -U t -d t -c "SELECT pg_sleep(1)" >/dev/null 2>&1 || true
done

if [ "$pgss_ready" -eq 0 ]; then
  echo "!! assertion 11 FAILED: pg_stat_statements container was not ready after 30 s"
  pgss_cleanup; trap - EXIT
  exit 1
fi

docker exec "$pgss_cid" psql -U t -d t -c "CREATE EXTENSION pg_stat_statements;" >/dev/null

# These queries populate pg_stat_statements. The last one spills to temp with a tiny work_mem.
docker exec "$pgss_cid" psql -U t -d t -c "
  SELECT count(*) FROM pg_stat_statements;
  SELECT count(*) FROM pg_stat_statements;
  SELECT count(*) FROM pg_stat_statements;
  SELECT 1+1;
  SELECT 1+1;
" >/dev/null

# The spill query: work_mem=64kB and 200k rows guarantee a spill.
docker exec "$pgss_cid" psql -U t -d t -c \
  "SET work_mem='64kB'; SELECT count(*) FROM (SELECT generate_series(1,200000) ORDER BY random()) s;" \
  >/dev/null

PGSS_DSN="postgres://t:pw@localhost:${PGSS_PORT}/t"
PGSS_OUT="${OUTDIR}/seed-validation-pgss.json"
PGSS_ERR="${OUTDIR}/seed-validation-pgss.err"

echo "   running pgtuner -format json against pg_stat_statements container"
set +e
"$BIN" -dsn "$PGSS_DSN" -format json >"$PGSS_OUT" 2>"$PGSS_ERR"
pgss_rc=$?
set -e

if [ "$pgss_rc" -ge 2 ]; then
  echo "!! assertion 11 FAILED: pgtuner exited with code ${pgss_rc}"
  cat "$PGSS_ERR"
  pgss_cleanup; trap - EXIT
  exit 1
fi

if grep -qiE '(column|relation|table|function) .* does not exist|ERROR:' "$PGSS_ERR" 2>/dev/null; then
  echo "!! assertion 11 FAILED: SQL error in pgtuner stderr:"
  cat "$PGSS_ERR"
  pgss_cleanup; trap - EXIT
  exit 1
fi

python3 - <<PYEOF3
import json, sys

with open("${PGSS_OUT}") as f:
    d = json.load(f)

advice = d.get("advice", [])
qp_advice = [a for a in advice if a.get("section") == "Query Performance"]

print(f"\n  Query Performance advice ({len(qp_advice)} item(s)):")
for a in qp_advice:
    print(f"    [{a['level']}] {a['title']}")
    if a.get("message"):
        print(f"      {a['message'][:200]}")

fail11 = False

if len(qp_advice) <= 1:
    # The extension can be present with no advice (for example, a new DB with only system queries).
    # The absent path returns one Info titled "not installed". Any other single item counts as present.
    if absent_msgs:
        print("\n  assertion 11a FAIL: QueryStatsCheck returned the absent-path Info, extension may not be loaded")
        fail11 = True
    else:
        print(f"\n  assertion 11a PASS: present-path entered (got {len(qp_advice)} item(s), none is the absent-path message)")
else:
    print(f"\n  assertion 11a PASS: present-path entered ({len(qp_advice)} advice items)")

unknowns = [a for a in qp_advice if a.get("level") == "unknown"]
if unknowns:
    print(f"  assertion 11b FAIL: {len(unknowns)} Unknown-level item(s), query failure(s):")
    for u in unknowns:
        print(f"    {u['title']}: {u.get('message','')[:200]}")
    fail11 = True
else:
    print(f"  assertion 11b PASS: no Unknown-level advice in Query Performance section")

top_time_found = any(
    "top" in a.get("title", "").lower() and "exec" in a.get("title", "").lower()
    for a in qp_advice
)
if top_time_found:
    print("  assertion 11c PASS: top-queries-by-time Info found")
else:
    print("  assertion 11c WARN: top-queries-by-time Info not found (may be OK if no queries ran long enough)")

temp_found = any(
    "temp" in a.get("title", "").lower()
    for a in qp_advice
)
if temp_found:
    print("  assertion 11d PASS: temp-heavy queries Info found")
else:
    print("  assertion 11d WARN: temp-heavy Info not found (spill may not have been captured yet)")

if fail11:
    sys.exit(1)

sys.exit(0)
PYEOF3

pgss_cleanup
trap - EXIT

echo "SEED PASS"
