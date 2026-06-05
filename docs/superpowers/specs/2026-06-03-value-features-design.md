# pgtuner Value-Features Design (Epics 1–7)

**Status:** approved roadmap (7 epics), full scope, no deferrals.
**Goal:** Take pgtuner from 14 baseline checks to a comprehensive PostgreSQL advisor covering environment/resource awareness, replication topology, schema/data-level analysis, workload, query performance, config depth, and richer reporting — all clean-room, derived from official PostgreSQL documentation.

**Clean-room mandate (applies to every epic):** No code, SQL, thresholds, or wording is taken from `postgresqltuner.pl` (GPLv3) or any GPL source. Every threshold/query/message is independently derived from the official PostgreSQL documentation and cited with a `// src:` comment. License stays Apache-2.0.

**Repo:** `github.com/OpenRemedy/pgtuner`, working clone at `/home/alberto/OpenRemedy/pgtuner`. Each epic: TDD, per-task spec+quality review, DCO-signed commits, pushed to `main`.

**Cross-cutting conventions (established in the baseline):**
- Each check implements `checks.Check` (`Name() string`, `Run(ctx, *Probe) []pgtuner.Advice`), never panics, never returns error; a query failure yields a single `Unknown`-level advice for that sub-check while siblings continue.
- Stats/query checks split a pure decision function (unit-tested exhaustively) from version/role-branched SQL wiring (integration-tested against live PG, and validated across PG 15/16/17/18 via `scripts/version-matrix.sh`).
- Settings come from the cached `pg_settings` snapshot (`Probe.Settings`); OS facts from `Probe.OS` (degrade gracefully on `ErrNoOSFacts`); the clock from `Probe.Now`.
- New advice levels reuse the existing `OK/Info/Todo/Warn/Bad/Unknown`.

---

## Shared infrastructure added up front (before Epic 1 checks)

These extend `Probe` and the inspector so every epic can rely on them.

1. **`Probe.Role` (replication role)** — populated once in `tuner.Analyze` (one round-trip) so role-aware checks don't each re-query. Type `pg.ServerRole` ∈ `{Primary, Standby, Standalone, Unknown}`, plus booleans `InRecovery bool`, `ConnectedStandbys int`. Derived from `pg_is_in_recovery()` and `pg_stat_replication`. Used by Epics 2 and 6.
2. **`osinspect.Inspector` extensions** — add to the interface:
   - `Environment() (Environment, error)` where `Environment{Kind EnvKind; CgroupVersion int}` and `EnvKind ∈ {BareMetal, VM, Docker, Kubernetes, LXC, Unknown}`.
   - `EffectiveMemoryBytes() (uint64, error)` — cgroup memory limit (v2 `memory.max`, v1 `memory.limit_in_bytes`) when constrained below host RAM; else host `MemTotal`. Returns the limit and whether it is a cgroup constraint.
   - `EffectiveCPUs() (float64, error)` — cgroup CPU quota (v2 `cpu.max`, v1 `cpu.cfs_quota_us/cpu.cfs_period_us`) when set; else `runtime.NumCPU()`.
   - `TransparentHugepages() (string, error)` — `/sys/kernel/mm/transparent_hugepage/enabled` active value (`always`/`madvise`/`never`).
   - `StorageDevices() ([]StorageDevice, error)` — for the PGDATA mount (when known) or root: rotational flag (`/sys/block/<dev>/queue/rotational`), filesystem type, mount options. Best-effort; empty on failure.
   `NoopInspector` returns `ErrNoOSFacts` for all. `LocalInspector` implements them for Linux (`/sys/fs/cgroup`, `/proc`, `/sys/block`).
   These are **only meaningful when pgtuner runs on the DB host** (`-os-inspect`); remote runs degrade to `Info` ("cannot determine without host access").

---

## Epic 1 — Environment & Resource Awareness

**Why:** The baseline memory/hugepages/autovacuum advice uses host `MemTotal` and `runtime.NumCPU()`. Inside a container with cgroup limits this is wrong (the "62 GiB" bug). This epic makes resource-relative advice correct and adds OS-level tuning checks.

**1.1 `environment.go` — `EnvironmentCheck` (section "Environment")**
- Report detected environment: BareMetal/VM/Docker/Kubernetes/LXC + cgroup version → **Info** (informational baseline). Detection signals: `/.dockerenv`, `/run/.containerenv`, cgroup paths containing `kubepods`/`docker`/`lxc`, `KUBERNETES_SERVICE_HOST` env, DMI/`systemd-detect-virt`-style hints from `/sys/class/dmi/id/product_name` (best-effort). src: derived from Linux kernel cgroup/namespace docs (cite kernel.org cgroup-v2 and the PG docs note on running in containers where applicable).
- If containerized AND `EffectiveMemoryBytes` < host `MemTotal`: **Info** stating the effective limit pgtuner will use for memory advice.
- Remote / no OS facts → single **Info** "environment detection requires running pgtuner on the DB host (-os-inspect)".

**1.2 Refactor memory/hugepages/autovacuum to use effective limits**
- `memory.go`: use `EffectiveMemoryBytes()` instead of `Memory().TotalBytes` for the shared_buffers/effective_cache_size/maintenance_work_mem ratios. Message states whether the figure is host RAM or a cgroup limit. (Behavior change, re-baseline the unit tests with a fake inspector returning a constrained limit.)
- `autovacuum.go`: `autovacuum_max_workers` advice considers `EffectiveCPUs()` (e.g. workers far exceeding CPUs → Info).
- `hugepages.go`: when containerized, add a **Info** that `vm.nr_hugepages` is host-global and typically not settable from inside an unprivileged container.

**1.3 `os_kernel.go` — `KernelCheck` (section "OS / Kernel")** (needs OS facts)
- **THP**: `/sys/kernel/mm/transparent_hugepage/enabled` == `always` → **Warn** (PostgreSQL recommends `never` or `madvise`; THP causes latency spikes). src: https://www.postgresql.org/docs/current/kernel-resources.html (huge pages) + the documented THP guidance.
- **vm.overcommit_memory**: not `2` on a dedicated DB host → **Info** (recommend `vm.overcommit_memory=2` + `overcommit_ratio` to avoid the OOM killer terminating the postmaster). src: https://www.postgresql.org/docs/current/kernel-resources.html#LINUX-MEMORY-OVERCOMMIT.
- **vm.swappiness**: high (>10) → **Info** (lower swappiness keeps the DB resident). src: kernel docs (labeled community practice).

**1.4 `storage_class.go` — extend the existing storage/planner advice** (needs OS facts)
- If the PGDATA device is non-rotational (SSD/NVMe) but `random_page_cost > 1.5` → upgrade the planner advice from a generic Info to a **targeted Info** citing the detected SSD. If rotational and `random_page_cost` lowered below ~2 → **Info** (may be too aggressive for HDD).
- Filesystem type Info when notable (e.g. detected ZFS → note `full_page_writes=off` is sometimes safe on ZFS; do NOT auto-recommend — Info only). Mount `noatime` absent on the PGDATA mount → **Info**.

---

## Epic 2 — Replication Role & Topology

**Why:** Advice must match the server's role; a standby and a primary have different correct settings.

**2.1 Role detection wired into `Probe.Role`** (shared infra above). `ReplicationCheck` reports the detected role as **Info** ("This server is a PRIMARY with N connected standbys" / "This server is a STANDBY (in recovery)" / "Standalone").

**2.2 Role-aware advice (extend `replication.go`)**
- **Standby** (`pg_is_in_recovery()`): check `hot_standby` (off → Info, queries can't run on the standby), `max_standby_streaming_delay`/`max_standby_archive_delay` (very low → Info about query cancellations; very high → Info about bloat on primary), `hot_standby_feedback` (off → Info about vacuum conflicts; on → Info about bloat tradeoff). Query the upstream via `pg_stat_wal_receiver` (status, latest LSN). src: https://www.postgresql.org/docs/current/hot-standby.html, runtime-config-replication.
- **Primary with standbys**: inspect `pg_stat_replication` — per-standby `write_lag`/`flush_lag`/`replay_lag`, `sync_state`. Significant lag → **Warn**. `synchronous_standby_names` set but no standby in `sync` state → **Warn**. `wal_keep_size`=0 with no slots while standbys exist → **Warn** (standby can desync if WAL is recycled). src: monitoring-stats pg_stat_replication.
- **Standalone**: keep current advice; note replication is unused.

**2.3 Replication type (physical vs logical)**
- Classify slots from `pg_replication_slots.slot_type` (physical/logical). Logical slots present → **Info** noting logical decoding; pair with `pg_publication`/`pg_subscription` counts. If this server is a **subscriber** (`pg_subscription` rows): **Info** + check subscription health via `pg_stat_subscription` (apply lag). src: https://www.postgresql.org/docs/current/logical-replication.html.
- `wal_level`: on a primary with logical slots or publications, `wal_level < logical` → **Bad** (logical replication won't work). src: runtime-config-wal.

---

## Epic 3 — Schema / Data-Level Checks

**Why:** The highest-value gap — analyzing actual data, not just GUCs. All queries scoped to the current database, with sane LIMITs and message capping.

New file `schema.go` (`SchemaCheck`, section "Schema") — multiple independent sub-checks, each query-failure-isolated:
- **Table bloat estimate** (heuristic from `pg_class.reltuples/relpages` + `pg_stats` avg row width vs actual relation size): tables with high estimated bloat % and non-trivial size → **Warn** (top offenders, capped). src: derived from the documented planner statistics columns (`pg_class`, `pg_statistic`/`pg_stats`); label the bloat formula as an estimate.
- **Index bloat estimate** (analogous, `pg_class` for index relpages vs expected) → **Info/Warn**. src as above.
- **Unused indexes**: `pg_stat_user_indexes.idx_scan = 0` AND index size > threshold AND not a unique/PK constraint → **Info** (drop candidates; note stats-reset caveat). src: monitoring-stats pg_stat_all_indexes.
- **Duplicate indexes**: indexes on identical `(indrelid, indkey, indclass, indoption, predicate)` → **Info**. src: pg_index catalog docs.
- **Invalid indexes**: `pg_index.indisvalid = false` or `indisready = false` → **Warn** (failed CONCURRENTLY build). src: https://www.postgresql.org/docs/current/sql-createindex.html (CONCURRENTLY).
- **Tables without a primary key** (user tables, size > threshold) → **Info** (replication/maintenance implications). src: ddl-constraints.
- **Foreign keys without a supporting index** on the referencing columns → **Info** (slow cascades / lock escalation). src: ddl-constraints (FK) + indexes docs.
- **Never-analyzed / never-vacuumed tables**: `pg_stat_user_tables.last_analyze IS NULL AND last_autoanalyze IS NULL` (and same for vacuum) on non-trivial tables → **Warn** (planner has no stats). src: monitoring-stats pg_stat_all_tables.
- **Largest tables/indexes** overview → **Info** (top N by `pg_total_relation_size`). src: database object size functions.

A `SchemaSummary` pure type aggregates per-sub-check results; each sub-check has a pure eval fn + unit tests, with a seed-data integration test (extend `scripts/seed-validation.sql`) creating bloat, an unused index, an invalid index, a PK-less table, and an FK-without-index to prove each fires.

---

## Epic 4 — Workload & Connection Analysis

New file `workload.go` (`WorkloadCheck`, section "Workload"). All from `pg_stat_activity` / lock views (point-in-time snapshot; messages note it's a snapshot).
- **Connection utilization**: `count(*) FROM pg_stat_activity` vs `max_connections` — >80% → **Warn**, >60% → **Info** (pooler advice). src: monitoring-stats pg_stat_activity.
- **Idle-in-transaction**: sessions in `idle in transaction` with `xact_start` age beyond a threshold → **Warn** (holds locks, blocks vacuum). Report count + oldest age. src: pg_stat_activity + the documented `idle_in_transaction_session_timeout`.
- **Long-running queries**: active queries with `query_start` age beyond a threshold → **Info** (report count + oldest). src: pg_stat_activity.
- **Blocked sessions / lock waits**: sessions waiting on locks (`wait_event_type='Lock'`) and the blocking PIDs via `pg_blocking_pids()` → **Warn** when present. src: https://www.postgresql.org/docs/current/monitoring-stats.html + functions-info `pg_blocking_pids`.
- **Dangling prepared transactions**: `pg_prepared_xacts` rows with old `prepared` time → **Warn** (block vacuum, hold locks). src: https://www.postgresql.org/docs/current/view-pg-prepared-xacts.html.
- Pure eval fns over snapshot structs; unit tests with synthetic rows; integration test against live PG (will mostly be OK on an idle dev DB) + a seed scenario opening an idle-in-transaction session to prove the Warn fires.

---

## Epic 5 — Query Performance (pg_stat_statements)

New file `querystats.go` (`QueryStatsCheck`, section "Query Performance"). Gated on the extension being present.
- Detect `pg_stat_statements` in `pg_extension` (or `shared_preload_libraries`). Absent → single **Info** (recommend installing; reuse the statistics-check guidance without duplicating advice — emit only the deep-dive here when present). 
- When present, query `pg_stat_statements` (handle the PG12→13 column rename introduced in pg_stat_statements 1.8: `total_time`→`total_exec_time`, `mean_time`→`mean_exec_time`; version-branch the SQL, gated `AtLeast(13,0)`):
  - **Top queries by total exec time** → **Info** (top N, normalized query text truncated). src: https://www.postgresql.org/docs/current/pgstatstatements.html.
  - **Low cache-hit queries**: `shared_blks_hit/(shared_blks_hit+shared_blks_read)` low with high reads → **Info**.
  - **Temp-heavy queries**: high `temp_blks_written` → **Info** (raise work_mem or fix the query).
- Pure eval fns over row structs; unit tests; integration test only runs the deep-dive when the extension exists (skip otherwise). Version-branch unit-tested for the column rename (PG≤12 vs ≥13).

---

## Epic 6 — Configuration Depth

Extend existing sections / add `config.go` (`ConfigCheck`, section "Configuration").
- **Pending restart**: `pg_settings WHERE pending_restart` → **Warn** (config changed but not applied; list names). src: https://www.postgresql.org/docs/current/view-pg-settings.html.
- **Parallelism vs CPUs** (uses `EffectiveCPUs()`): `max_worker_processes`, `max_parallel_workers`, `max_parallel_workers_per_gather` sanity vs effective CPU count — all-zero parallel-per-gather on a multi-CPU box → **Info**; parallel workers > CPUs → **Info**. src: runtime-config-resource (async behavior / workers).
- **Logging hygiene**: `log_min_duration_statement = -1` → **Info** (no slow-query logging); `log_checkpoints` off (<PG15 default) → **Info**; `log_lock_waits` off → **Info**; `log_autovacuum_min_duration = -1` → **Info**; `log_temp_files` already covered in storage (don't duplicate). src: runtime-config-logging.
- **Timeouts**: `statement_timeout=0` → **Info** (consider a ceiling); `idle_in_transaction_session_timeout=0` → **Info** (pairs with the Epic-4 idle-in-txn finding); `lock_timeout=0` → **Info**; PG≥17 `transaction_timeout=0` → **Info**. src: runtime-config-client.
- **Archiving**: if `archive_mode` on, check `pg_stat_archiver` — `failed_count > 0` or `last_failed_time > last_archived_time` → **Warn**. src: monitoring-stats pg_stat_archiver.
- **Extensions & versions**: `pg_available_extensions WHERE installed_version IS NOT NULL AND installed_version <> default_version` → **Info** (run `ALTER EXTENSION ... UPDATE`). src: https://www.postgresql.org/docs/current/sql-alterextension.html.
- Each is a settings- or single-query sub-check with a pure eval fn + unit tests; integration test against live PG.

---

## Epic 7 — Reporting / UX

Extend `cmd/pgtuner` and `internal/report`, plus `pkg/pgtuner`.
- **Level filtering**: `-min-level` flag (e.g. `warn`) filters advice below the threshold in both renderers. Pure filter fn on `[]Advice` + unit test.
- **Severity sorting**: `-sort` flag (`section` default | `severity`) — severity sort orders Bad→Warn→Todo→Info→OK→Unknown within the output. Pure sort + test.
- **Summary grade/score**: add `Report.Grade()` deriving a letter/score (e.g. A–F) from counts (Bad heavily weighted) — documented formula, pure, unit-tested. Terminal footer + JSON `summary.grade`.
- **Per-database analysis**: `-all-databases` flag — iterate user databases (from `pg_database WHERE NOT datistemplate AND datallowconn`), running the per-database checks (cache/schema/storage temp/autovacuum dead-tuples/workload) against each, grouped in the report by database. Cluster-wide checks (memory, wal, version, env) run once. `Report` gains an optional per-database breakdown. Integration test against the live cluster (which has ≥1 db).
- Update README + JSON schema docs to describe the new fields/flags.

---

## Testing & validation (all epics)
- Unit tests for every pure eval fn (boundaries explicit).
- `//go:build integration` tests against the live PG16 for every query path.
- `scripts/version-matrix.sh` must still end MATRIX PASS on PG 15/16/17/18 after every epic, with **zero spurious `unknown`** from missing columns (every version branch verified). Extend the matrix to also assert no panics and stable section set.
- `scripts/seed-validation.sql` extended per Epic 3/4 to prove data-level and workload findings actually fire.
- After all epics: full `go build ./... && go vet ./... && go test ./... && integration && matrix` green; README and integration doc updated; nothing left unimplemented.

## Ordering & dependencies
Shared infra (Probe.Role, inspector extensions) → Epic 1 → 2 → 3 → 4 → 5 → 6 → 7. Epics 1 and the shared infra unblock the resource-aware parts of 6 (parallelism vs CPU) and 2 (role). Each epic ships to `main` independently and leaves the suite green.
