package checks

import (
	"context"
	"fmt"

	"github.com/soylic/pgtuner/internal/pg"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// pg_stat_io was introduced in PG16.
const docPGStatIO = "https://www.postgresql.org/docs/16/monitoring-stats.html#MONITORING-PG-STAT-IO-VIEW"

// ioClientEvictionWarnThreshold is the minimum count of client-backend evictions
// (context='normal') before a Warn. Such evictions mean the background writer lags.
// The absolute floor avoids false positives on idle or new servers.
// src: https://www.postgresql.org/docs/16/monitoring-stats.html#MONITORING-PG-STAT-IO-VIEW
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-BGWRITER-LRU-MAXPAGES
const ioClientEvictionWarnThreshold int64 = 1000

// ioClientFsyncWarnThreshold is the minimum count of client-backend fsyncs before a Warn.
// Only the checkpointer and WAL writer fsync in a healthy cluster. The absolute floor avoids false positives.
const ioClientFsyncWarnThreshold int64 = 100

// ioClientWriteWarnThreshold is the minimum count of client-backend writes
// (context='normal') before a Warn. This is a heuristic: high counts correlate with a small
// shared_buffers or checkpoint_completion_target needing adjustment. It uses the eviction threshold scale.
// src: https://www.postgresql.org/docs/16/monitoring-stats.html#MONITORING-PG-STAT-IO-VIEW
const ioClientWriteWarnThreshold int64 = 1000

// IORow holds one pg_stat_io row. Only client-backend rows are queried; context
// distinguishes normal, bulkread, bulkwrite, and vacuum I/O.
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

// evalIO takes pre-filtered client-backend rows. It uses only the 'normal' context,
// where evictions and writes indicate shared_buffers pressure and fsyncs indicate checkpoint lag.
// src: https://www.postgresql.org/docs/16/monitoring-stats.html#MONITORING-PG-STAT-IO-VIEW
func evalIO(rows []IORow) []pgtuner.Advice {
	const section = "I/O"
	var totalEvictions, totalFsyncs, totalWrites int64
	for _, r := range rows {
		// bulkread and bulkwrite use separate buffer rings and are expected to evict.
		// Vacuum uses its own buffer strategy.
		if r.BackendType == "client backend" && r.Context == "normal" {
			totalEvictions += r.Evictions
			totalFsyncs += r.Fsyncs
			totalWrites += r.Writes
		}
	}

	var out []pgtuner.Advice

	// Client-backend evictions mean bgwriter is not cleaning pages proactively.
	// src: https://www.postgresql.org/docs/16/monitoring-stats.html#MONITORING-PG-STAT-IO-VIEW
	if totalEvictions >= ioClientEvictionWarnThreshold {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Warn,
			Section: section,
			Title:   "Client backends are evicting shared buffers",
			Message: fmt.Sprintf("pg_stat_io shows that client backends performed %d evictions in the 'normal' context. In a well-tuned cluster, the background writer cleans pages proactively, so client sessions never need to evict. A high eviction count indicates shared_buffers pressure or an under-configured bgwriter.", totalEvictions),
			Detail:  "Consider increasing shared_buffers, raising bgwriter_lru_maxpages, or lowering bgwriter_delay. These changes let the background writer keep up with the write rate.",
			DocRef:  docPGStatIO,
		})
	}

	// Client backends should never fsync in normal operation.
	// src: https://www.postgresql.org/docs/16/monitoring-stats.html#MONITORING-PG-STAT-IO-VIEW
	if totalFsyncs >= ioClientFsyncWarnThreshold {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Warn,
			Section: section,
			Title:   "Client backends are performing fsyncs",
			Message: fmt.Sprintf("pg_stat_io shows that client backends performed %d fsyncs. Fsyncs from client backends indicate that the checkpointer or WAL writer cannot flush dirty data fast enough. Foreground sessions then flush it directly, which increases query latency.", totalFsyncs),
			Detail:  "Reduce checkpoint interval pressure by raising max_wal_size or lowering checkpoint_completion_target from the default. Review I/O subsystem throughput and consider dedicated WAL storage.",
			DocRef:  docPGStatIO,
		})
	}

	// Like evictions, high client-backend writes indicate shared_buffers pressure or a lagging background writer.
	// src: https://www.postgresql.org/docs/16/monitoring-stats.html#MONITORING-PG-STAT-IO-VIEW
	if totalWrites >= ioClientWriteWarnThreshold {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Warn,
			Section: section,
			Title:   "Client backends are writing buffers at a high rate",
			Message: fmt.Sprintf("pg_stat_io shows that client backends performed %d writes in the 'normal' context. High write counts from client backends indicate shared_buffers pressure. Foreground sessions write dirty data to the kernel page cache instead of the background writer absorbing that work.", totalWrites),
			Detail:  "Consider increasing shared_buffers, raising bgwriter_lru_maxpages, or lowering bgwriter_delay. Also review max_wal_size and checkpoint_completion_target if checkpoint writes contribute.",
			DocRef:  docPGStatIO,
		})
	}

	return out
}

// Run queries pg_stat_io (PG16+). On PG<16 it emits one Info without querying.
// A query or scan failure yields one Unknown advice.
// It never panics and never returns an error.
func (IOCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "I/O"

	if !p.Version.AtLeast(16, 0) {
		return []pgtuner.Advice{{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "pg_stat_io unavailable on this PostgreSQL version",
			Message: fmt.Sprintf("pg_stat_io was introduced in PostgreSQL 16. This server runs version %s. Upgrade to PostgreSQL 16 or later for detailed per-backend-type I/O statistics.", p.Version),
			DocRef:  docPGStatIO,
		}}
	}

	// Other backend types (bgwriter, checkpointer, walwriter) evict and fsync by design.
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

	// PG18 added asynchronous I/O.
	// src: https://www.postgresql.org/docs/18/runtime-config-resource.html#GUC-IO-METHOD
	if p.Version.AtLeast(18, 0) {
		out = append(out, evalIOMethodPG18(p.Settings)...)
	}

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.OK,
			Section: section,
			Title:   "I/O load looks healthy",
			Message: "pg_stat_io shows no evidence of excessive client-backend evictions or fsyncs. The background writer and checkpointer appear to handle I/O work as expected.",
			DocRef:  docPGStatIO,
		})
	}
	return out
}

// evalIOMethodPG18 returns Info advice for notable io_method and io_workers values on PG18+.
// The PG18 defaults are io_method=worker and io_workers=3 (boot_val), so they produce no advice.
// 'sync' disables async I/O and 'io_uring' depends on the kernel, so both get an Info.
// io_workers is flagged only with io_method=worker and a value other than 3.
// src: https://www.postgresql.org/docs/18/runtime-config-resource.html#GUC-IO-METHOD
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
		// GUC absent: PG<18 or settings not loaded.

	case method == defaultIOMethod:

	case method == "sync":
		// 'sync' may reduce write throughput.
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "Async I/O is disabled (io_method=sync, PG18+)",
			Message: "io_method=sync disables asynchronous I/O. The PostgreSQL 18 default is 'worker', which enables asynchronous I/O through worker processes for better write throughput. Verify that this was intentional. Sync mode may be appropriate for debugging or specific storage configurations, but is not recommended for general workloads.",
			DocRef:  docIOMethod,
		})

	case method == "io_uring":
		// Valid on kernels with full io_uring support (Linux ≥5.1, preferably ≥5.10).
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "io_uring async I/O backend is active (PG18+)",
			Message: "io_method=io_uring uses the Linux io_uring interface for asynchronous I/O. This requires a sufficiently recent kernel (Linux ≥5.1, with ≥5.10 recommended for stability). Verify kernel support and confirm that the setting is intentional for this host.",
			DocRef:  docIOMethod,
		})

	default:
		// Unknown future value.
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "Unrecognised io_method value (PG18+)",
			Message: fmt.Sprintf("io_method=%q is not a value that this advisor recognises. Verify that it is a valid PostgreSQL 18 io_method setting.", method),
			DocRef:  docIOMethod,
		})
	}

	// Workers are unused with io_method=sync, so no advice there.
	// src: https://www.postgresql.org/docs/18/runtime-config-resource.html#GUC-IO-WORKERS
	if method == defaultIOMethod || method == "" {
		if workers, ok := s.Int("io_workers"); ok && workers != defaultIOWorkers {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Info,
				Section: section,
				Title:   "io_workers is set to a non-default value (PG18+)",
				Message: fmt.Sprintf("io_workers=%d (default is %d). This controls the number of I/O worker processes used when io_method=worker. Verify that the value is appropriate for your storage subsystem and workload concurrency.", workers, defaultIOWorkers),
				DocRef:  docIOWorkers,
			})
		}
	}

	return out
}
