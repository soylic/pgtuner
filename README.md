# pgtuner

A PostgreSQL configuration tuning advisor — connects to a running server,
inspects its settings and runtime statistics, and reports actionable advice
across 22 check areas with graded severity levels and an overall A–F grade.

## License

Apache-2.0. Copyright 2026 OpenRemedy. See `LICENSE`.

## Requirements

- Go 1.25+ (build from source) or a pre-built binary
- Network access to the PostgreSQL server
- A database user with `pg_monitor` membership or equivalent read access

## Install / Build

```bash
git clone https://github.com/OpenRemedy/pgtuner
cd pgtuner
go build -o pgtuner ./cmd/pgtuner
```

Or install directly:

```bash
go install github.com/OpenRemedy/pgtuner/cmd/pgtuner@latest
```

## Usage

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-dsn` | `""` | libpq connection string, e.g. `postgres://user:pass@host:5432/db` |
| `-host` | `""` | PostgreSQL host (alternative to `-dsn`) |
| `-port` | `5432` | PostgreSQL port |
| `-user` | `""` | PostgreSQL user |
| `-dbname` | `""` | PostgreSQL database name |
| `-format` | `terminal` | Output format: `terminal` (alias `text`), `json`, `csv`, or `markdown` (alias `md`). Case-insensitive |
| `-os-inspect` | false | Gather host memory and sysctl facts (Linux only; must run on the DB host) |
| `-no-color` | false | Disable ANSI colour in terminal output. Only affects `-format terminal`; it is a no-op for `json`/`csv`/`markdown` |
| `-min-level` | `ok` | Only show advice at or above this severity (`ok`/`info`/`todo`/`warn`/`bad`/`unknown`). Filters the displayed advice only; the summary counts and grade always reflect the full set. |
| `-sort` | `section` | Advice ordering: `section` (group by section, first-seen order) or `severity` (a flat list ordered Bad→Warn→Todo→Info→OK→Unknown, each item still labelled with its section) |
| `-all-databases` | false | Run per-database checks against every connectable, non-template database; cluster-wide checks run once. Each per-database finding is tagged with its database. |
| `-version` | false | Print the tool version and exit |

When `-dsn` is omitted, pgtuner builds a connection string from any combination
of `-host`, `-port`, `-user`, and `-dbname`. Remaining connection parameters
(password, SSL mode, etc.) fall back to the standard `PG*` environment variables
and `~/.pgpass`.

### Report Grade

Every report carries an overall letter grade (A–F) summarising the server's
health. It is a pure function of the per-level advice counts:

```
infoPenalty    = min(2*info, 10)
unknownPenalty = min(3*unknown, 9)
score = 100 - 25*bad - 8*warn - infoPenalty - unknownPenalty   (clamped to [0, 100])
grade = A if score >= 90, B if >= 80, C if >= 70, D if >= 60, else F
```

`ok` and `todo` carry no penalty. `bad` is weighted heavily so a single
`bad`-level finding caps the grade at C (and two reach F); `unknown` carries a
small penalty as an undetermined coverage gap. The aggregate `info` and
`unknown` penalties are capped (at 10 and 9 respectively) so a server with no
`bad` and few `warn` findings cannot fail purely on the volume of informational
findings. The grade appears in the terminal footer and as `grade` in the JSON
output.

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Analysis complete; no `bad`-level advice |
| 1 | Analysis complete; at least one `bad`-level advice was found |
| 2 | Tool-level error (connection failure, flag parse error, etc.) |

### Example Invocations

```bash
# Using a full DSN
pgtuner -dsn 'postgres://analyst@db.example.com:5432/mydb'

# Using individual flags
pgtuner -host db.example.com -user analyst -dbname mydb

# Machine-readable JSON output (useful in CI or for downstream tooling)
pgtuner -dsn 'postgres://analyst@db.example.com:5432/mydb' -format json

# CSV table of findings (one row per advice item; no grade/summary)
pgtuner -dsn 'postgres://analyst@db.example.com:5432/mydb' -format csv

# Markdown report (paste straight into a PR or issue)
pgtuner -dsn 'postgres://analyst@db.example.com:5432/mydb' -format markdown

# With OS-level inspection (run on the database host itself)
pgtuner -dsn 'postgres://analyst@localhost:5432/mydb' -os-inspect

# Show only warnings and worse, ordered by severity
pgtuner -dsn "$DB_DSN" -min-level warn -sort severity

# Analyze every user database in the cluster (per-database checks tagged per db)
pgtuner -dsn "$DB_DSN" -all-databases

# Check exit code in a CI pipeline
pgtuner -dsn "$DB_DSN" -format json > report.json || echo "Bad-level advice found"
```

## Sample Output

### Terminal (colour disabled)

```
PostgreSQL Tuning Report — server 16.13

=== Connection ===
  [warn]  Listening on all interfaces without SSL — listen_addresses='*' with ssl=off exposes the PostgreSQL port on every network interface without encryption.
          Detail: Set ssl=on and configure ssl_cert_file/ssl_key_file, or restrict listen_addresses to trusted interfaces.
          Ref:    https://www.postgresql.org/docs/current/runtime-config-connection.html
  [info]  Cannot verify RAM headroom (no OS facts) — Worst-case backend memory is approximately 0.6 GiB (max_connections=100 × (work_mem=4096 kB + 2 MiB baseline)). Verify this fits within available RAM.
          Ref:    https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-WORK-MEM

=== Version ===
  [ok]    PostgreSQL 16 is supported — Running PostgreSQL 16.13; community support continues until 2028-11-09.
          Ref:    https://www.postgresql.org/support/versioning/

=== Memory ===
  [info]  shared_buffers (no OS facts) — shared_buffers is set to 128 MiB; cannot compute ratio without total RAM.
          Ref:    https://www.postgresql.org/docs/current/runtime-config-resource.html

=== Cache ===
  [ok]    Cache hit ratios look healthy — Buffer cache hit ratio is ≥99%, index-block hit ratio is ≥95%, and table-block hit ratio is ≥95% (or no activity to measure). No cache-pressure concerns detected.
          Ref:    https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-DATABASE-VIEW

=== WAL ===
  [info]  WAL compression is disabled — wal_compression=off; enabling it reduces WAL volume (less disk I/O, smaller replication lag) at the cost of a small amount of CPU at WAL-write time.
          Detail: Consider setting wal_compression=lz4 or wal_compression=zstd for better throughput. Both lz4 and zstd are available on PostgreSQL 15+.
          Ref:    https://www.postgresql.org/docs/15/runtime-config-wal.html#GUC-WAL-COMPRESSION

...

Summary:  8 ok  23 info  0 todo  1 warn  0 bad  0 unknown   Grade: F
```

### Per-database (`-all-databases`)

With `-all-databases`, cluster-wide sections (Connection, Memory, WAL, …) appear
once, while per-database sections (Cache, Storage, Autovacuum, Schema, Workload,
Query Performance) repeat per database with each finding tagged `[db]`:

```
=== Cache ===
  [ok]    [openremedy] Cache hit ratios look healthy — …
  [ok]    [openremedy_test] Cache hit ratios look healthy — …
  [warn]  [postgres] Buffer cache hit ratio is low — Buffer cache hit ratio is 98.85% …
```

### JSON (`-format json`)

```json
{
  "server_version": "16.13",
  "advice": [
    {
      "level": "warn",
      "section": "Connection",
      "title": "Listening on all interfaces without SSL",
      "message": "listen_addresses='*' with ssl=off exposes the PostgreSQL port on every network interface without encryption.",
      "detail": "Set ssl=on and configure ssl_cert_file/ssl_key_file, or restrict listen_addresses to trusted interfaces.",
      "doc_ref": "https://www.postgresql.org/docs/current/runtime-config-connection.html"
    },
    {
      "level": "ok",
      "section": "Version",
      "title": "PostgreSQL 16 is supported",
      "message": "Running PostgreSQL 16.13; community support continues until 2028-11-09.",
      "doc_ref": "https://www.postgresql.org/support/versioning/"
    }
  ],
  "summary": {
    "bad": 0,
    "info": 23,
    "ok": 8,
    "todo": 0,
    "unknown": 0,
    "warn": 1
  },
  "grade": "F"
}
```

Under `-all-databases` each per-database finding gains a `"database"` field, and
the report adds a top-level `"databases"` array listing every analyzed database:

```json
{
  "server_version": "16.13",
  "advice": [
    {
      "level": "warn",
      "section": "Cache",
      "title": "Buffer cache hit ratio is low",
      "message": "Buffer cache hit ratio is 98.85% …",
      "doc_ref": "https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-DATABASE-VIEW",
      "database": "postgres"
    }
  ],
  "summary": { "ok": 30, "info": 40, "warn": 3, "bad": 0, "todo": 0, "unknown": 0 },
  "grade": "F",
  "databases": ["openremedy", "openremedy_test", "postgres"]
}
```

Cluster-wide findings (and every finding in a default single-database run) omit
the `database` field; `databases` is omitted entirely without `-all-databases`.

### CSV (`-format csv`)

A clean, importable table of findings — one row per advice item, in the
displayed order. The header is fixed and the grade/summary are intentionally
omitted (read those from the terminal or JSON output):

```csv
database,section,level,title,message,detail,doc_ref
,Connection,warn,Listening on all interfaces without SSL,"listen_addresses='*' with ssl=off exposes the PostgreSQL port on every network interface without encryption.",Set ssl=on and configure ssl_cert_file/ssl_key_file.,https://www.postgresql.org/docs/current/runtime-config-connection.html
,Version,ok,PostgreSQL 16 is supported,Running PostgreSQL 16.13; community support continues until 2028-11-09.,,https://www.postgresql.org/support/versioning/
```

Under `-all-databases` the `database` column carries the per-database tag;
cluster-wide rows leave it empty.

### Markdown (`-format markdown`)

A report formatted for pasting into a PR or issue. Advice is grouped by section
(first-seen order) into tables, with Detail/Ref rendered as sub-lines:

```markdown
# PostgreSQL Tuning Report — server 16.13

**Grade: F**

Summary: 8 ok · 23 info · 0 todo · 1 warn · 0 bad · 0 unknown

### Connection

| Level | Title | Message |
|-------|-------|---------|
| warn | Listening on all interfaces without SSL | listen_addresses='*' with ssl=off exposes the PostgreSQL port... |

**Listening on all interfaces without SSL**
- _Detail:_ Set ssl=on and configure ssl_cert_file/ssl_key_file.
- _Ref:_ https://www.postgresql.org/docs/current/runtime-config-connection.html
```

## PostgreSQL Support Matrix

| Major | EOL Date | Version-specific handling |
|-------|----------|--------------------------|
| 14 | 2026-11-12 | Baseline; `pg_stat_activity`-based stats |
| 15 | 2027-11-11 | Stats collector moved to shared memory; `stats_temp_directory` removed — pgtuner skips that setting on PG15+ |
| 16 | 2028-11-09 | `pg_stat_io` available; I/O check uses it for eviction/fsync analysis |
| 17 | 2029-11-08 | `pg_stat_checkpointer` gains split columns (`num_requested`, `num_timed`, `write_time`, etc.); pgtuner uses the new column names on PG17+ |
| 18 | 2030-11-14 | Data checksums enabled by default; async I/O (`io_method`) available — pgtuner notes these as positive signals when detected |

Validated against live PG 15, 16, 17, and 18 instances via `scripts/version-matrix.sh`.

## What It Checks

pgtuner runs 22 checks in the following order. Each check has a *scope*:
**cluster-wide** checks examine instance-level state (settings, OS, replication
topology) and run once; **per-database** checks examine a single database's
catalog and stats, and under `-all-databases` run once per user database.

| # | Check | Scope | What it inspects |
|---|-------|-------|------------------|
| 1 | **Connection** | cluster | `max_connections`, `work_mem` headroom vs. available RAM, listen/SSL exposure |
| 2 | **Version** | cluster | community support window; flags end-of-life versions |
| 3 | **Environment** | cluster | bare-metal/VM/Docker/Kubernetes/LXC detection, cgroup memory/CPU limits |
| 4 | **Memory** | cluster | `shared_buffers`, `effective_cache_size`, `maintenance_work_mem` ratios (cgroup-aware) |
| 5 | **Huge Pages** | cluster | `huge_pages` setting and `vm.nr_hugepages` kernel value (with OS inspection) |
| 6 | **OS / Kernel** | cluster | transparent huge pages, `vm.overcommit_memory`, `vm.swappiness` (with OS inspection) |
| 7 | **Cache** | per-db | buffer cache hit ratio, index-block hit ratio, table-block hit ratio |
| 8 | **WAL** | cluster | `wal_compression`, `wal_level`, `min_wal_size`/`max_wal_size` |
| 9 | **Checkpoints** | cluster | requested vs. timed checkpoint ratio, `checkpoint_completion_target` |
| 10 | **Planner** | cluster | `random_page_cost`, `default_statistics_target`, storage-class-aware tuning |
| 11 | **Storage Settings** | cluster | data checksums, TOAST compression, `log_temp_files` |
| 12 | **Storage** | per-db | temp-file spills (`pg_stat_database.temp_files`/`temp_bytes`) |
| 13 | **I/O** | cluster | `pg_stat_io` eviction/fsync pressure (PG16+), background writer efficiency |
| 14 | **Autovacuum Settings** | cluster | enabled, `autovacuum_max_workers`, worker slots (PG18+), failsafe age (PG14+), workers vs CPUs |
| 15 | **Autovacuum** | per-db | dead-tuple accumulation, transaction-ID wraparound age |
| 16 | **Security** | cluster | superuser count, `password_encryption`, public schema privileges |
| 17 | **Statistics** | cluster | `track_io_timing`, `pg_stat_statements` presence |
| 18 | **Replication** | cluster | role/topology, standby lag, slots, logical replication, WAL spill |
| 19 | **Schema** | per-db | bloat estimates, unused/duplicate/invalid indexes, PK-less tables, unindexed FKs, never-analyzed tables |
| 20 | **Workload** | per-db | connection utilization, idle-in-transaction, long-running queries, lock waits, prepared xacts |
| 21 | **Query Performance** | per-db | `pg_stat_statements` top queries by time, low cache-hit and temp-heavy queries |
| 22 | **Configuration** | cluster | pending restart, parallelism vs CPUs, logging hygiene, timeouts, archiving health, stale extensions |

## Advice Levels

Each piece of advice carries one of these levels:

| Level | Meaning |
|-------|---------|
| `ok` | Setting is within recommended parameters |
| `info` | Low-priority suggestion |
| `todo` | Improvement worth scheduling |
| `warn` | Suboptimal configuration |
| `bad` | Dangerous or high-risk setting (causes exit code 1) |
| `unknown` | Check could not determine a result |

## Library Use

pgtuner is also importable as a Go package:

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/OpenRemedy/pgtuner/pkg/pgtuner"
    "github.com/OpenRemedy/pgtuner/pkg/tuner"
)

func main() {
    ctx := context.Background()
    rep, err := tuner.Analyze(ctx, pgtuner.Options{
        DSN: "postgres://analyst@localhost:5432/mydb",
    })
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Server: %s\n", rep.ServerVersion)
    for _, a := range rep.Advice {
        if a.Level >= pgtuner.Warn {
            fmt.Printf("[%s] %s: %s\n", a.Level, a.Section, a.Title)
        }
    }

    // rep.ExitCode() returns 1 if any Bad-level advice is present, else 0.
    fmt.Printf("Summary: %v\n", rep.Summary)
}
```

The `Report` struct:

```go
type Report struct {
    ServerVersion string         `json:"server_version"`
    Advice        []Advice       `json:"advice"`
    Summary       map[string]int `json:"summary"`             // level name -> count
    GradeLetter   string         `json:"grade"`               // overall A–F grade; read via Grade()
    Databases     []string       `json:"databases,omitempty"` // analyzed databases (-all-databases only)
}
```

Set `Options.AllDatabases` to run per-database checks against every user
database; per-database `Advice` items carry a `Database` field. Use
`pgtuner.FilterByMinLevel`, `pgtuner.SortBySeverity`, and `Report.Grade()` to
post-process the report.

## OS Inspection

Pass `-os-inspect` when running pgtuner **on the database host itself**. On
Linux it reads `/proc/meminfo` and relevant `sysctl` values (`vm.nr_hugepages`,
`vm.overcommit_memory`, etc.) to enrich the memory and huge-pages checks.

Without `-os-inspect` (or on non-Linux hosts) those checks degrade gracefully:
they emit `info`-level advice noting that OS facts are unavailable, rather than
failing or silently skipping. Remote analysis — connecting to a server on
another host — is fully supported; it simply produces less precise memory
recommendations.

## Validation

`scripts/version-matrix.sh` spins up disposable Docker containers for PG 15,
16, 17, and 18 on alternate ports (5515–5518), runs `pgtuner -format json`
against each, validates the JSON, and checks that no `unknown`-level advice appears in
the summary. It exits with `MATRIX PASS` when all versions pass.

```bash
bash scripts/version-matrix.sh
```

Requires Docker and Python 3.

## Contributing

All contributions must be DCO-signed (`git commit -s`).
