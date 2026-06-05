package checks

import (
	"context"
	"fmt"

	"github.com/OpenRemedy/pgtuner/internal/pg"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// docPGStatIO is the canonical reference for pg_stat_io (introduced in PG16).
// src: https://www.postgresql.org/docs/16/monitoring-stats.html#MONITORING-PG-STAT-IO-VIEW
const docPGStatIO = "https://www.postgresql.org/docs/16/monitoring-stats.html#MONITORING-PG-STAT-IO-VIEW"

// ioClientEvictionWarnThreshold is the minimum number of evictions by client
// backends (backend_type='client backend', context='normal') required before
// we emit a Warn. Evictions from shared_buffers by client backends mean the
// background writer is not keeping up, forcing foreground sessions to do I/O
// work that should be offloaded. We require a non-trivial absolute count to
// avoid false positives on idle or newly started servers where counters are
// near zero.
// Heuristic origin: pg_stat_io documentation notes that evictions from the
// 'normal' context by client backends indicate shared_buffers pressure; an
// absolute threshold of 1000 before warning avoids alerting on insignificant
// transient activity. Community practice treats sustained client-backend
// evictions as a sign that bgwriter_lru_maxpages or shared_buffers needs tuning.
// src: https://www.postgresql.org/docs/16/monitoring-stats.html#MONITORING-PG-STAT-IO-VIEW
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-BGWRITER-LRU-MAXPAGES
const ioClientEvictionWarnThreshold int64 = 1000

// ioClientFsyncWarnThreshold is the minimum number of fsyncs by client
// backends before we emit a Warn. In a healthy cluster fsyncs are performed
// exclusively by the checkpointer and WAL writer. Client-backend fsyncs occur
// when those processes fall behind, forcing foreground sessions to flush dirty
// pages to disk directly. We require a non-trivial absolute count for the
// same false-positive-avoidance reason as evictions.
// src: https://www.postgresql.org/docs/16/monitoring-stats.html#MONITORING-PG-STAT-IO-VIEW
const ioClientFsyncWarnThreshold int64 = 100

// ioClientWriteWarnThreshold is the minimum number of writes by client
// backends (backend_type='client backend', context='normal') before we emit a
// Warn. Client backends writing buffers directly to the kernel page cache
// means shared_buffers is under pressure: pages are being dirtied and flushed
// by foreground sessions rather than being absorbed by the background writer.
// This is a heuristic signal, not a definitive measure; high write counts
// correlate with shared_buffers being too small or checkpoint_completion_target
// needing adjustment. We use the same absolute threshold scale as evictions to
// avoid alerting on idle or newly started servers.
// Heuristic origin: pg_stat_io writes column documents backend writes to the
// kernel buffer cache; sustained high counts from client backends indicate
// shared_buffers pressure similar to the eviction signal.
// src: https://www.postgresql.org/docs/16/monitoring-stats.html#MONITORING-PG-STAT-IO-VIEW
const ioClientWriteWarnThreshold int64 = 1000

// IORow holds a single aggregated row of interest from pg_stat_io.
// Only client-backend rows (backend_type='client backend') are queried; the
// context column distinguishes normal, bulkread, bulkwrite, and vacuum I/O.
type IORow struct {
	BackendType string
	Context     string
	Evictions   int64
	Fsyncs      int64
	Writes      int64
}

// IOCheck evaluates I/O pressure using pg_stat_io (PG16+).
type IOCheck struct{ clusterScoped }

func (IOCheck) Name() string { return "io" }

// evalIO is the pure decision function for pg_stat_io advice.
// It accepts pre-fetched rows (already filtered to client backend) and returns
// advice without touching the database.
//
// The heuristic focuses on the 'normal' context for client backends: evictions,
// fsyncs, and writes in that context indicate shared_buffers pressure and
// checkpoint lag respectively. All three require non-trivial absolute counts to
// avoid false positives on idle systems.
//
// src: https://www.postgresql.org/docs/16/monitoring-stats.html#MONITORING-PG-STAT-IO-VIEW
func evalIO(rows []IORow) []pgtuner.Advice {
	const section = "I/O"
	var totalEvictions, totalFsyncs, totalWrites int64
	for _, r := range rows {
		// Only the 'normal' context for client backends is relevant to this
		// heuristic: bulkread/bulkwrite contexts use different buffer rings and
		// are expected to evict, while vacuum uses its own buffer strategy.
		if r.BackendType == "client backend" && r.Context == "normal" {
			totalEvictions += r.Evictions
			totalFsyncs += r.Fsyncs
			totalWrites += r.Writes
		}
	}

	var out []pgtuner.Advice

	// Evictions: client backends evicting dirty pages from shared_buffers means
	// bgwriter is not keeping up. The background writer should clean pages
	// proactively so client backends never need to evict.
	// src: https://www.postgresql.org/docs/16/monitoring-stats.html#MONITORING-PG-STAT-IO-VIEW
	if totalEvictions >= ioClientEvictionWarnThreshold {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Warn,
			Section: section,
			Title:   "Client backends are evicting shared buffers",
			Message: fmt.Sprintf("pg_stat_io shows client backends have performed %d evictions in the 'normal' context. In a well-tuned cluster the background writer cleans pages proactively so client sessions never need to evict; a high eviction count indicates shared_buffers pressure or an under-configured bgwriter.", totalEvictions),
			Detail:  "Consider increasing shared_buffers, raising bgwriter_lru_maxpages, or lowering bgwriter_delay to allow the background writer to keep up with the write rate.",
			DocRef:  docPGStatIO,
		})
	}

	// Fsyncs: client backends calling fsync directly means the checkpointer or
	// WAL writer is not flushing dirty data fast enough. In normal operation
	// client backends should never fsync.
	// src: https://www.postgresql.org/docs/16/monitoring-stats.html#MONITORING-PG-STAT-IO-VIEW
	if totalFsyncs >= ioClientFsyncWarnThreshold {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Warn,
			Section: section,
			Title:   "Client backends are performing fsyncs",
			Message: fmt.Sprintf("pg_stat_io shows client backends have performed %d fsyncs. Fsyncs from client backends indicate the checkpointer or WAL writer cannot flush dirty data fast enough, forcing foreground sessions to do so directly — increasing query latency.", totalFsyncs),
			Detail:  "Reduce checkpoint interval pressure by raising max_wal_size or lowering checkpoint_completion_target from the default. Review I/O subsystem throughput and consider dedicated WAL storage.",
			DocRef:  docPGStatIO,
		})
	}

	// Writes: client backends writing buffers to the kernel page cache at a high
	// rate indicates that shared_buffers is absorbing fewer dirty pages than the
	// workload generates, causing foreground sessions to flush data themselves.
	// This mirrors the eviction signal: both indicate shared_buffers pressure or
	// a background writer that cannot keep up with the write rate.
	// Heuristic: the writes column in pg_stat_io tracks kernel page cache writes
	// by each backend_type/context combination; sustained high counts from client
	// backends in the 'normal' context correlate with the same conditions that
	// drive eviction pressure.
	// src: https://www.postgresql.org/docs/16/monitoring-stats.html#MONITORING-PG-STAT-IO-VIEW
	if totalWrites >= ioClientWriteWarnThreshold {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Warn,
			Section: section,
			Title:   "Client backends are writing buffers at a high rate",
			Message: fmt.Sprintf("pg_stat_io shows client backends have performed %d writes in the 'normal' context. High write counts from client backends indicate shared_buffers pressure: foreground sessions are writing dirty data to the kernel page cache instead of that work being absorbed by the background writer.", totalWrites),
			Detail:  "Consider increasing shared_buffers, raising bgwriter_lru_maxpages, or lowering bgwriter_delay. Also review max_wal_size and checkpoint_completion_target if checkpoint writes are contributing.",
			DocRef:  docPGStatIO,
		})
	}

	return out
}

// Run queries pg_stat_io (PG≥16) and returns I/O-pressure advice.
// On PG<16 it emits a single Info without querying the database.
// On query or scan failure it returns a single Unknown-level advice.
// It never panics and never returns an error.
func (IOCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "I/O"

	// pg_stat_io was introduced in PostgreSQL 16.
	// src: https://www.postgresql.org/docs/16/monitoring-stats.html#MONITORING-PG-STAT-IO-VIEW
	if !p.Version.AtLeast(16, 0) {
		return []pgtuner.Advice{{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "pg_stat_io unavailable on this PostgreSQL version",
			Message: fmt.Sprintf("pg_stat_io was introduced in PostgreSQL 16. This server is running version %s. Upgrade to PostgreSQL 16 or later for detailed per-backend-type I/O statistics.", p.Version),
			DocRef:  docPGStatIO,
		}}
	}

	// Query client-backend rows only; other backend types (bgwriter, checkpointer,
	// walwriter) performing evictions and fsyncs is expected and healthy.
	// src: https://www.postgresql.org/docs/16/monitoring-stats.html#MONITORING-PG-STAT-IO-VIEW
	const sql = `SELECT backend_type, context,
		COALESCE(evictions, 0), COALESCE(fsyncs, 0), COALESCE(writes, 0)
		FROM pg_stat_io
		WHERE backend_type = 'client backend'`

	dbRows, err := p.Conn.Query(ctx, sql)
	if err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "pg_stat_io query failed",
			Message: fmt.Sprintf("Could not query pg_stat_io for I/O statistics: %v", err),
		}}
	}
	defer dbRows.Close()

	var rows []IORow
	for dbRows.Next() {
		var r IORow
		if err := dbRows.Scan(&r.BackendType, &r.Context, &r.Evictions, &r.Fsyncs, &r.Writes); err != nil {
			return []pgtuner.Advice{{
				Level:   pgtuner.Unknown,
				Section: section,
				Title:   "pg_stat_io scan failed",
				Message: fmt.Sprintf("Could not scan pg_stat_io row: %v", err),
			}}
		}
		rows = append(rows, r)
	}
	if err := dbRows.Err(); err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "pg_stat_io iteration failed",
			Message: fmt.Sprintf("Error iterating pg_stat_io rows: %v", err),
		}}
	}

	out := evalIO(rows)

	// PG≥18: surface io_method and io_workers when set to non-default values.
	// Asynchronous I/O is a new capability in PG18; operators who have enabled
	// it will find it useful to see the effective configuration surfaced.
	// src: https://www.postgresql.org/docs/18/runtime-config-resource.html#GUC-IO-METHOD
	if p.Version.AtLeast(18, 0) {
		out = append(out, evalIOMethodPG18(p.Settings)...)
	}

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.OK,
			Section: section,
			Title:   "I/O load looks healthy",
			Message: "pg_stat_io shows no evidence of excessive client-backend evictions or fsyncs. Background writer and checkpointer appear to be handling I/O work as expected.",
			DocRef:  docPGStatIO,
		})
	}
	return out
}

// evalIOMethodPG18 returns Info advice for notable io_method and io_workers
// configurations on PG≥18.
//
// In PostgreSQL 18 the default io_method is 'worker' (boot_val="worker",
// io_workers boot_val=3). The 'worker' method enables asynchronous I/O via
// worker processes and is the intended production default; no advice is emitted
// for it. The 'sync' method disables async I/O and is an intentional
// regression from the default; an Info is emitted so operators are aware.
// The 'io_uring' method is an alternative async backend with a kernel
// dependency; an Info is emitted to flag the kernel dependency.
//
// io_workers is only flagged when io_method=worker is active and the value
// differs from the default of 3, so that operators who have explicitly tuned
// the worker count see an informational note.
//
// src: https://www.postgresql.org/docs/18/runtime-config-resource.html#GUC-IO-METHOD
// (default io_method=worker, io_workers=3 confirmed against PG18 boot_val)
func evalIOMethodPG18(s *pg.Settings) []pgtuner.Advice {
	const (
		section          = "I/O"
		docIOMethod      = "https://www.postgresql.org/docs/18/runtime-config-resource.html#GUC-IO-METHOD"
		docIOWorkers     = "https://www.postgresql.org/docs/18/runtime-config-resource.html#GUC-IO-WORKERS"
		defaultIOMethod  = "worker"
		defaultIOWorkers = 3
	)
	var out []pgtuner.Advice

	method, methodKnown := s.Get("io_method")

	switch {
	case !methodKnown:
		// GUC not present (PG<18 settings snapshot or settings not loaded) — no advice.

	case method == defaultIOMethod:
		// 'worker' is the PG18 default; async I/O is active as expected. Stay quiet.

	case method == "sync":
		// 'sync' disables asynchronous I/O entirely. This is a deliberate step
		// away from the PG18 default and may reduce write throughput.
		// src: https://www.postgresql.org/docs/18/runtime-config-resource.html#GUC-IO-METHOD
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "Async I/O is disabled (io_method=sync, PG18+)",
			Message: "io_method=sync disables asynchronous I/O. The PostgreSQL 18 default is 'worker', which enables asynchronous I/O via worker processes for better write throughput. Verify this was intentional — sync mode may be appropriate for debugging or specific storage configurations, but is not recommended for general workloads.",
			DocRef:  docIOMethod,
		})

	case method == "io_uring":
		// 'io_uring' is an alternative async backend that depends on kernel support.
		// Informational only: it is not the default but is a valid choice on
		// kernels with full io_uring support (Linux ≥5.1, preferably ≥5.10).
		// src: https://www.postgresql.org/docs/18/runtime-config-resource.html#GUC-IO-METHOD
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "io_uring async I/O backend is active (PG18+)",
			Message: "io_method=io_uring uses the Linux io_uring interface for asynchronous I/O. This requires a sufficiently recent kernel (Linux ≥5.1; ≥5.10 recommended for stability). Verify kernel support and confirm the setting is intentional for this host.",
			DocRef:  docIOMethod,
		})

	default:
		// Unknown future value — emit a neutral Info so operators can investigate.
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "Unrecognised io_method value (PG18+)",
			Message: fmt.Sprintf("io_method=%q is not a value recognised by this advisor. Verify it is a valid PostgreSQL 18 io_method setting.", method),
			DocRef:  docIOMethod,
		})
	}

	// io_workers: only relevant when io_method=worker (the default or explicit).
	// When set to a value other than the default 3, emit an informational note.
	// No advice when io_method=sync (workers are unused) or when absent.
	// src: https://www.postgresql.org/docs/18/runtime-config-resource.html#GUC-IO-WORKERS
	if method == defaultIOMethod || method == "" {
		if workers, ok := s.Int("io_workers"); ok && workers != defaultIOWorkers {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Info,
				Section: section,
				Title:   "io_workers is set to a non-default value (PG18+)",
				Message: fmt.Sprintf("io_workers=%d (default is %d). This controls the number of I/O worker processes used when io_method=worker. Verify the value is appropriate for your storage subsystem and workload concurrency.", workers, defaultIOWorkers),
				DocRef:  docIOWorkers,
			})
		}
	}

	return out
}
