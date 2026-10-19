# pgtuner

[![CI](https://github.com/soylic/pgtuner/actions/workflows/ci.yml/badge.svg)](https://github.com/soylic/pgtuner/actions/workflows/ci.yml)

pgtuner connects to a running PostgreSQL server, reads its settings and runtime
statistics, and tells you what to change. Each finding has a severity and a link
to the PostgreSQL documentation. The report ends with an A to F grade.

It also tells you where the server sits. It shows if the server is a primary,
a standby or a cascading standby, and which nodes replicate from it. It shows
if Patroni, repmgr, pg_auto_failover or Citus manages it. It shows if the
server is a hosted service such as RDS or Cloud SQL.

pgtuner only reads. It never changes a setting.

## Install

Download a binary, `.deb` or `.rpm` from the
[releases page](https://github.com/soylic/pgtuner/releases). Each release
includes a `SHA256SUMS` file.

Or build it. You need Go 1.25 or later:

```bash
go install github.com/soylic/pgtuner/cmd/pgtuner@latest
```

The database user needs `pg_monitor` membership or equivalent read access.
Some findings need more. Reading `pg_control_system()` for the system identifier
needs a grant outside superuser, and `pg_stat_activity` shows other users'
sessions only to `pg_read_all_stats`. Without those, the matching field is
empty and the rest of the report still works.

## Quick start

```bash
pgtuner -dsn 'postgres://analyst@db.example.com:5432/mydb'
```

The password comes from `PGPASSWORD` or `~/.pgpass`, like any libpq client.

```
PostgreSQL Tuning Report, server 17.11

=== Findings (by severity) ===
  (Connection)
  [warn]  SSL disabled: ssl=off. The server transmits all client traffic (including passwords) in plaintext.
          Detail: Enable SSL to encrypt client-server traffic.
          Ref:    https://www.postgresql.org/docs/current/runtime-config-connection.html
  (Replication)
  [warn]  Inactive replication slot(s) retain WAL and can fill the disk: pg_replication_slots shows 1 inactive slot(s): pg2. ...
          Detail: Drop unused slots with SELECT pg_drop_replication_slot('<name>'). ...
          Ref:    https://www.postgresql.org/docs/current/view-pg-replication-slots.html

Summary:  7 ok  28 info  0 todo  3 warn  0 bad  0 unknown   Grade: D
```

That output comes from `pgtuner -min-level warn -sort severity`, trimmed.

## Options

| Flag | Default | Meaning |
|------|---------|---------|
| `-dsn` | | libpq connection string, such as `postgres://user@host:5432/db` |
| `-host`, `-port`, `-user`, `-dbname` | `5432` for the port | Build a connection from parts when `-dsn` is empty |
| `-format` | `terminal` | `terminal` (alias `text`), `json`, `csv` or `markdown` (alias `md`). Case-insensitive |
| `-os-inspect` | off | Read host memory and sysctl values. Linux only. Run it on the database host |
| `-no-color` | off | Turn off ANSI color. Affects `terminal` only |
| `-min-level` | `ok` | Show advice at this level or worse: `ok`, `info`, `todo`, `warn`, `bad`, `unknown`. The summary and grade still count everything |
| `-sort` | `section` | `section` groups by section. `severity` prints one list, worst first |
| `-all-databases` | off | Run per-database checks on every connectable, non-template database. Cluster-wide checks run once |
| `-version` | | Print the version and exit |

When `-dsn` is empty, pgtuner builds one from `-host`, `-port`, `-user` and
`-dbname`. The password, SSL mode and other parameters come from the `PG*`
environment variables and `~/.pgpass`.

### Exit codes

| Code | Meaning |
|------|---------|
| 0 | Analysis finished. No `bad` advice |
| 1 | Analysis finished. At least one `bad` advice |
| 2 | pgtuner failed: bad flag, no connection, and so on |

The exit code uses the full advice set, even when `-min-level` hides some of it.

```bash
pgtuner -dsn "$DB_DSN" -format json > report.json || echo "bad-level advice found"
```

## Where is this server in the cluster?

Every report includes a Topology section, and the JSON report has `topology`
and `flavor` objects. pgtuner reads them from the server it connects to. It does
not connect to other nodes.

| Question | How pgtuner answers it |
|----------|------------------------|
| Primary or standby? | `pg_is_in_recovery()` and `pg_stat_replication` |
| Who feeds this standby? | `pg_stat_wal_receiver`, then `primary_conninfo`. pgtuner reads only the host, port and node name from `primary_conninfo`. It never stores the password |
| Who replicates from this server? | `pg_stat_replication`: name, address, state and sync state |
| Is this a cascading standby? | A standby that has its own downstream nodes |
| Are two nodes in the same cluster? | They share `system_identifier` |
| Did a failover happen? | The `timeline` number goes up on each promotion |
| Which tool manages it? | See below |
| Which distribution is it? | `version()`, `server_version`, provider settings and roles, and `aurora_version()` |

**Cluster managers.** Three of them have a catalog that pgtuner can read, so a
match is `certain` and the report lists the members from that catalog:

- repmgr: the table `repmgr.nodes`
- pg_auto_failover: the table `pgautofailover.node`, on the monitor database
- Citus: the table `pg_dist_node`

Patroni keeps its state in etcd, Consul or ZooKeeper, not in PostgreSQL. pgtuner
looks for its marks instead: sessions named `Patroni heartbeat`, and a
replication slot named like the standby that uses it. A match is `probable` or
`possible`, never `certain`. The report says so.

**Distributions.** pgtuner names Aurora, RDS, Cloud SQL, AlloyDB, Azure, Supabase,
Neon, YugabyteDB, CockroachDB, Greenplum and EDB. It also lists the
`timescaledb` and `citus` extensions and the packager text in `server_version`,
for example `Ubuntu 17.11-1.pgdg22.04+2`. On a hosted service the report warns
that `ALTER SYSTEM` and kernel advice may not apply.

What the tests cover:

| Part | Tested against |
|------|----------------|
| Roles, cascade, upstream, downstream, system identifier | A live Patroni 4.1.5 cluster of three nodes with etcd 3.5, one node cascading from another |
| Patroni marks | The same cluster |
| repmgr, pg_auto_failover, Citus | Unit tests only. The catalog queries have not run against a real installation |
| Hosted-service names | Unit tests only. The setting prefixes and role names come from each provider's conventions, which they can change |

Example, on a cascading standby managed by Patroni (`-format json`, trimmed):

```json
{
  "topology": {
    "role": "cascading_standby",
    "system_identifier": "7698463095641292832",
    "timeline": 1,
    "cluster_name": "demo",
    "node_name": "pg1",
    "upstream": { "host": "pg2", "port": 5432, "state": "streaming", "slot": "pg1" },
    "downstream": [
      { "name": "pg3", "host": "172.23.0.4", "state": "streaming", "sync_state": "async" }
    ],
    "manager": {
      "name": "patroni",
      "confidence": "probable",
      "evidence": [
        "1 session(s) with application_name starting with 'Patroni'",
        "cluster_name=\"demo\" and a replication slot is named like its standby (pg3, pg1)"
      ]
    }
  },
  "flavor": { "name": "postgresql", "managed": false, "packaging": "Ubuntu 17.11-1.pgdg22.04+2" }
}
```

To see a whole group, run pgtuner on each node and compare `system_identifier`.

## What it checks

pgtuner runs 23 checks. A cluster-wide check reads instance state and runs once.
A per-database check reads one database. With `-all-databases` it runs once for
each database, and every finding carries the database name.

| # | Check | Scope | What it looks at |
|---|-------|-------|------------------|
| 1 | Connection | cluster | `max_connections`, `work_mem` against RAM, listen and SSL exposure |
| 2 | Version | cluster | Community support window and end-of-life |
| 3 | Topology | cluster | Role, neighbours, cluster manager, distribution |
| 4 | Environment | cluster | Bare metal, VM, Docker, Kubernetes or LXC. cgroup memory and CPU limits |
| 5 | Memory | cluster | `shared_buffers`, `effective_cache_size`, `maintenance_work_mem` against RAM. cgroup-aware |
| 6 | Huge Pages | cluster | `huge_pages` and `vm.nr_hugepages` |
| 7 | OS / Kernel | cluster | Transparent huge pages, `vm.overcommit_memory`, `vm.swappiness` |
| 8 | Cache | per-db | Buffer, index and table hit ratios |
| 9 | WAL | cluster | `wal_compression`, `wal_level`, `min_wal_size`, `max_wal_size` |
| 10 | Checkpoints | cluster | Requested against timed checkpoints, `checkpoint_completion_target` |
| 11 | Planner | cluster | `random_page_cost`, `default_statistics_target`, storage class |
| 12 | Storage Settings | cluster | Data checksums, TOAST compression, `log_temp_files` |
| 13 | Storage | per-db | Temp-file spills |
| 14 | I/O | cluster | `pg_stat_io` eviction and fsync pressure (PG16+), background writer |
| 15 | Autovacuum Settings | cluster | Enabled, worker count and slots, failsafe age, workers against CPUs |
| 16 | Autovacuum | per-db | Dead tuples, transaction ID wraparound age |
| 17 | Security | cluster | Superuser count, `password_encryption`, `public` schema privileges |
| 18 | Statistics | cluster | `track_io_timing`, `pg_stat_statements` |
| 19 | Replication | cluster | Slots, lag, standby settings, logical replication, WAL spill |
| 20 | Schema | per-db | Bloat, unused, duplicate and invalid indexes, tables without a primary key, foreign keys without an index, never-analyzed tables |
| 21 | Workload | per-db | Connection use, idle-in-transaction sessions, long queries, lock waits, prepared transactions |
| 22 | Query Performance | per-db | Top queries by time, low cache hit, heavy temp use |
| 23 | Configuration | cluster | Pending restart, parallelism, logging, timeouts, archiving, outdated extensions |

### PostgreSQL versions

pgtuner supports PostgreSQL 14 to 18 and adjusts its queries to each version.

| Major | End of life | What changes |
|-------|-------------|--------------|
| 14 | 2026-11-12 | Baseline |
| 15 | 2027-11-11 | `stats_temp_directory` no longer exists, so pgtuner skips it |
| 16 | 2028-11-09 | `pg_stat_io` exists, and the I/O check uses it |
| 17 | 2029-11-08 | `pg_stat_checkpointer` has new column names, and pgtuner uses them |
| 18 | 2030-11-14 | Data checksums on by default. `io_method` is available |

CI runs the integration tests against 14, 15, 16, 17 and 18.

## Levels and grade

| Level | Meaning |
|-------|---------|
| `ok` | The setting is fine |
| `info` | Low priority |
| `todo` | Worth scheduling |
| `warn` | Not a good setting |
| `bad` | Dangerous. Sets exit code 1 |
| `unknown` | pgtuner could not decide |

The grade depends only on the count of findings at each level:

```
infoPenalty    = min(2 * info, 10)
unknownPenalty = min(3 * unknown, 9)
score = 100 - 25 * bad - 8 * warn - infoPenalty - unknownPenalty   (0 to 100)
grade = A if score >= 90, B if >= 80, C if >= 70, D if >= 60, else F
```

`ok` and `todo` cost nothing. One `bad` finding caps the grade at C, and two give
an F. The `info` and `unknown` penalties have caps, so many low-priority
findings cannot fail a server alone.

## Output formats

`-format json` has `server_version`, `advice`, `summary`, `grade`, `topology` and
`flavor`. With `-all-databases` it adds `databases`, and each per-database advice
has a `database` field.

`-format csv` writes one row per advice, with the header
`database,section,level,title,message,detail,doc_ref`. It has no grade or summary.

`-format markdown` groups advice by section into tables, ready to paste into an
issue or pull request.

## Use it as a Go library

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/soylic/pgtuner/pkg/pgtuner"
	"github.com/soylic/pgtuner/pkg/tuner"
)

func main() {
	rep, err := tuner.Analyze(context.Background(), pgtuner.Options{
		DSN: "postgres://analyst@localhost:5432/mydb",
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("server", rep.ServerVersion, "grade", rep.Grade())
	if rep.Topology != nil {
		fmt.Println("role", rep.Topology.Role)
	}
	for _, a := range rep.Advice {
		if a.Level >= pgtuner.Warn {
			fmt.Printf("[%s] %s: %s\n", a.Level, a.Section, a.Title)
		}
	}
	// rep.ExitCode() is 1 when any advice is bad, else 0.
}
```

Set `Options.AllDatabases` to run per-database checks everywhere. Use
`pgtuner.FilterByMinLevel` and `pgtuner.SortBySeverity` to shape the advice list.
`Report.Topology` and `Report.Flavor` are nil only in reports you build yourself.

## OS inspection

With `-os-inspect`, pgtuner reads `/proc/meminfo` and a few `sysctl` values on
Linux. Use it only when pgtuner runs on the database host. The memory, huge page
and kernel checks then compare PostgreSQL settings against real host values.

Without it, or on another operating system, those checks still run and report
`info` advice that OS facts are missing. Remote analysis works. The memory
advice is less precise.

## Develop

```bash
go build ./...
go vet ./...
go test ./...
```

Integration tests need a database and the `integration` build tag:

```bash
docker run -d --rm --name pgtuner-test -p 55432:5432 \
  -e POSTGRES_USER=pgtuner -e POSTGRES_PASSWORD=pgtuner -e POSTGRES_DB=pgtuner \
  postgres:17-alpine
PGTUNER_TEST_DSN='postgres://pgtuner:pgtuner@localhost:55432/pgtuner' \
  go test -tags integration ./...
```

`scripts/version-matrix.sh` starts a disposable container for each of PostgreSQL
14 to 18 on ports 5514 to 5518. It runs `pgtuner -format json` against each one.
It checks that the JSON is valid and that no advice is `unknown`. It needs Docker
and Python 3.

To make a release, push a tag such as `v0.1.0`. The release workflow builds
binaries for Linux, macOS and Windows, plus `.deb` and `.rpm` packages, and
publishes them with a `SHA256SUMS` file.
