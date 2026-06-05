# pgtuner — OpenRemedy Integration

pgtuner is licensed under Apache-2.0, so OpenRemedy (FSL-1.1-ALv2) can import it
directly as a Go package dependency.

## Consumption Modes

### Mode A — Go package import

Import `github.com/OpenRemedy/pgtuner` directly. Call `tuner.Analyze` and
work with the returned `*pgtuner.Report` struct in-process.

```go
import (
    "context"

    "github.com/OpenRemedy/pgtuner/pkg/pgtuner"
    "github.com/OpenRemedy/pgtuner/pkg/tuner"
)

func RunTuner(ctx context.Context, dsn string) (*pgtuner.Report, error) {
    return tuner.Analyze(ctx, pgtuner.Options{
        DSN:          dsn,
        OSInspect:    false, // set true only when running on the DB host
        AllDatabases: false, // set true to analyze every user database
    })
}
```

The `Report` fields that matter downstream:

| Field | Type | Description |
|-------|------|-------------|
| `ServerVersion` | `string` | PostgreSQL version string, e.g. `"16.13"` |
| `Advice` | `[]pgtuner.Advice` | Ordered list of findings |
| `Summary` | `map[string]int` | Per-level count, e.g. `{"ok":8,"warn":1}` |
| `GradeLetter` | `string` (JSON `grade`) | Overall A–F grade; read via `Report.Grade()` |
| `Databases` | `[]string` | Databases analyzed under `-all-databases` (omitted otherwise) |

Each `Advice` item:

| Field | Type | Description |
|-------|------|-------------|
| `Level` | `pgtuner.Level` | Severity (`ok`, `info`, `todo`, `warn`, `bad`, `unknown`) |
| `Section` | `string` | Check area, e.g. `"Memory"`, `"WAL"` |
| `Title` | `string` | Short finding title |
| `Message` | `string` | Full human-readable explanation |
| `Detail` | `string` | Remediation guidance (may be empty) |
| `DocRef` | `string` | PostgreSQL documentation URL for the relevant GUC/view |
| `Database` | `string` | Database the finding pertains to; set only for per-database checks under `-all-databases`, empty otherwise (JSON `database`, omitempty) |

`Level` serialises to/from its lowercase string name in JSON
(`"ok"`, `"info"`, `"todo"`, `"warn"`, `"bad"`, `"unknown"`).

`Report.ExitCode()` returns `1` if any advice is `Bad`-level, `0` otherwise —
useful for gating deployment pipelines.

### Grade

`Report.Grade()` returns a letter (`A`–`F`) derived purely from the per-level
counts. The `info` and `unknown` penalties are capped so a long tail of minor
findings can't sink the grade on its own:

```
infoPenalty    = min(2*info, 10)
unknownPenalty = min(3*unknown, 9)
score = clamp(100 - 25*bad - 8*warn - infoPenalty - unknownPenalty, 0, 100)
A>=90, B>=80, C>=70, D>=60, else F
```

`ok`/`todo` carry no penalty.
The grade is precomputed into `GradeLetter` by `NewReport` and serialised as the
JSON `grade` field. It is a stable, deterministic summary suitable for trend
tracking across runs.

### Output controls and per-database analysis

- **`-min-level <level>`** filters the *displayed* advice to a minimum severity.
  The `Summary` counts and `grade` always reflect the full set, so filtering
  never hides a `bad` from the exit code or grade. The pure helper
  `pgtuner.FilterByMinLevel(advice, min)` is available in Mode A.
- **`-sort severity`** orders advice Bad→Warn→Todo→Info→OK→Unknown
  (`pgtuner.SortBySeverity`). Default `-sort section` groups by section.
- **`-all-databases`** (`Options.AllDatabases`) runs cluster-wide checks once
  and per-database checks against every connectable, non-template database from
  `pg_database`. Per-database findings carry a `database` tag; the report's
  top-level `databases` array lists every analyzed database. This is the right
  mode when a single OpenRemedy-managed PostgreSQL instance hosts multiple
  application databases.

### Mode B — Subprocess with `-format json`

Run the `pgtuner` binary with `-format json` and parse its stdout. This is appropriate
when the OpenRemedy component is written in a language other than Go, or when
keeping pgtuner at a separate release cadence is preferred.

```bash
pgtuner -dsn "$DSN" -format json > report.json
exit_code=$?
# exit_code 0 = no bad advice; 1 = bad advice present; 2 = tool error
```

The JSON output is a single object matching the `Report` struct above:

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

Under `-all-databases`, per-database advice items add a `"database"` field and
the report gains a top-level `"databases"` array:

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

Cluster-wide findings omit `database`; `databases` is absent without
`-all-databases`. Parse with any standard JSON library; the `level` values are
stable lowercase strings defined in `pkg/pgtuner/advice.go`.

## Suggested Integration Points

- **Server registration / health checks**: call `tuner.Analyze` (or the binary)
  after a new PostgreSQL server is registered to produce an initial tuning
  snapshot that appears in the server detail view.
- **Proactive loop**: run pgtuner on a schedule (e.g. daily via the
  `PatrolScheduler`) and create OpenRemedy incidents for any `bad`-level advice
  that was not present at the previous run.
- **Recipe execution context**: surface the pgtuner `Summary` as a pre-flight
  check before executing configuration-change recipes, so operators can see the
  current tuning state before applying changes.

## No Code Changes Required

Consuming pgtuner in Mode A requires only adding it to `go.mod`:

```bash
go get github.com/OpenRemedy/pgtuner@latest
```

No changes to OpenRemedy's existing packages, database schema, or API surface
are needed to add pgtuner as a dependency.
