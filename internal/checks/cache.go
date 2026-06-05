package checks

import (
	"context"
	"fmt"

	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// docPGStatDatabase is the canonical reference for pg_stat_database columns.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-DATABASE-VIEW
const docPGStatDatabase = "https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-DATABASE-VIEW"

// docPGStatioUserIndexes is the canonical reference for pg_statio_user_indexes.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STATIO-ALL-INDEXES-VIEW
const docPGStatioUserIndexes = "https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STATIO-ALL-INDEXES-VIEW"

// docPGStatioUserTables is the canonical reference for pg_statio_user_tables.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STATIO-ALL-TABLES-VIEW
const docPGStatioUserTables = "https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STATIO-ALL-TABLES-VIEW"

// cacheHitRatioWarnThreshold is the buffer cache hit ratio below which we warn.
// A ratio below 0.99 (99 %) means more than 1 % of block requests are served
// from disk rather than the shared-buffer cache. Community guidance and the PG
// documentation both treat a sustained ratio below 99 % as an indicator that
// shared_buffers may be undersized for the working set.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-DATABASE-VIEW
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-SHARED-BUFFERS
const cacheHitRatioWarnThreshold = 0.99 // 99 %

// indexHitRatioInfoThreshold is the index-block hit ratio below which we emit
// an Info. Index pages that are not in shared_buffers are read from disk on
// every index scan; a ratio below 0.95 (95 %) is a soft indicator that
// shared_buffers may not retain the frequently used index pages.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STATIO-ALL-INDEXES-VIEW
const indexHitRatioInfoThreshold = 0.95 // 95 %

// tableHitRatioInfoThreshold is the heap-block hit ratio below which we emit
// an Info. Heap (table) pages not found in shared_buffers must be read from
// disk on every table access; a ratio below 0.95 (95 %) is a soft indicator
// that frequently-accessed tables may not be staying in shared_buffers.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STATIO-ALL-TABLES-VIEW
const tableHitRatioInfoThreshold = 0.95 // 95 %

// CacheCheck evaluates shared-buffer and index-block cache hit ratios.
type CacheCheck struct{ databaseScoped }

func (CacheCheck) Name() string { return "cache" }

// evalCacheHitRatio is the pure decision function for buffer-cache hit-ratio
// advice. It accepts pre-fetched blks_hit and blks_read from pg_stat_database
// and returns advice without touching the database.
//
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-DATABASE-VIEW
func evalCacheHitRatio(blksHit, blksRead int64) []pgtuner.Advice {
	const section = "Cache"

	total := blksHit + blksRead
	if total == 0 {
		return []pgtuner.Advice{{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "No buffer activity recorded yet",
			Message: "pg_stat_database shows zero block accesses for the current database since statistics were last reset. This is normal on a freshly started or stats-reset server.",
			DocRef:  docPGStatDatabase,
		}}
	}

	ratio := float64(blksHit) / float64(total)
	if ratio < cacheHitRatioWarnThreshold {
		return []pgtuner.Advice{{
			Level:   pgtuner.Warn,
			Section: section,
			Title:   "Buffer cache hit ratio is low",
			Message: fmt.Sprintf("Buffer cache hit ratio is %.2f%% (blks_hit=%d, blks_read=%d). Requests served from disk rather than shared_buffers exceed 1%%, which may indicate shared_buffers is undersized for the working set or that the working set is too large to cache.", ratio*100, blksHit, blksRead),
			Detail:  "Consider increasing shared_buffers (commonly 25–40% of RAM for a dedicated PG host) or review query access patterns to reduce the effective working set.",
			DocRef:  docPGStatDatabase,
		}}
	}
	return nil
}

// evalIndexHitRatio is the pure decision function for index-block cache hit
// advice. It accepts nullable SUM(idx_blks_hit) and SUM(idx_blks_read) from
// pg_statio_user_indexes and returns advice without touching the database.
// Both inputs are pointers so that a NULL result from SUM over an empty table
// set can be represented and handled gracefully (NULL SUM → no indexes → no advice).
//
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STATIO-ALL-INDEXES-VIEW
func evalIndexHitRatio(idxHit, idxRead *int64) []pgtuner.Advice {
	const section = "Cache"

	// NULL SUM means there are no user indexes to measure; skip.
	if idxHit == nil || idxRead == nil {
		return nil
	}

	total := *idxHit + *idxRead
	if total == 0 {
		// Indexes exist but have never been accessed since stats reset; skip.
		return nil
	}

	ratio := float64(*idxHit) / float64(total)
	if ratio < indexHitRatioInfoThreshold {
		return []pgtuner.Advice{{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "Index block cache hit ratio is low",
			Message: fmt.Sprintf("Index-block cache hit ratio is %.2f%% (idx_blks_hit=%d, idx_blks_read=%d). More than 5%% of index-page requests are satisfied from disk; frequently used index pages may not be retained in shared_buffers.", ratio*100, *idxHit, *idxRead),
			Detail:  "Increasing shared_buffers allows more index pages to stay in cache. Consider also reviewing which indexes are heavily accessed via pg_stat_user_indexes.",
			DocRef:  docPGStatioUserIndexes,
		}}
	}
	return nil
}

// evalTableHitRatio is the pure decision function for heap-block (table) cache
// hit advice. It accepts nullable SUM(heap_blks_hit) and SUM(heap_blks_read)
// from pg_statio_user_tables and returns advice without touching the database.
// Both inputs are pointers so that a NULL result from SUM over an empty table
// set can be represented and handled gracefully (NULL SUM → no tables → no advice).
//
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STATIO-ALL-TABLES-VIEW
func evalTableHitRatio(heapHit, heapRead *int64) []pgtuner.Advice {
	const section = "Cache"

	// NULL SUM means there are no user tables to measure; skip.
	if heapHit == nil || heapRead == nil {
		return nil
	}

	total := *heapHit + *heapRead
	if total == 0 {
		// Tables exist but have never been accessed since stats reset; skip.
		return nil
	}

	ratio := float64(*heapHit) / float64(total)
	if ratio < tableHitRatioInfoThreshold {
		return []pgtuner.Advice{{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "Table block cache hit ratio is low",
			Message: fmt.Sprintf("Table cache hit ratio is %.2f%% (heap_blks_hit=%d, heap_blks_read=%d). More than 5%% of table-page requests are satisfied from disk; frequently-accessed tables may not be staying in shared_buffers.", ratio*100, *heapHit, *heapRead),
			Detail:  "Increasing shared_buffers allows more table pages to stay in cache. Consider also reviewing which tables are heavily accessed via pg_statio_user_tables.",
			DocRef:  docPGStatioUserTables,
		}}
	}
	return nil
}

// Run queries buffer-cache and index-block hit ratios and returns advice.
// On query or scan failure it returns a single Unknown-level advice for the
// failing sub-check; the other sub-check still runs.
// It never panics and never returns an error.
func (CacheCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "Cache"
	var out []pgtuner.Advice

	// --- buffer cache (pg_stat_database) ----------------------------------------
	// blks_hit: disk-block requests served from shared_buffers.
	// blks_read: disk-block requests that required a physical read.
	// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-DATABASE-VIEW
	const bufSQL = "SELECT blks_hit, blks_read FROM pg_stat_database WHERE datname = current_database()"
	var blksHit, blksRead int64
	if err := p.Conn.QueryRow(ctx, bufSQL).Scan(&blksHit, &blksRead); err != nil {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Buffer cache stats unavailable",
			Message: fmt.Sprintf("Could not query pg_stat_database for buffer-cache statistics: %v", err),
		})
	} else {
		out = append(out, evalCacheHitRatio(blksHit, blksRead)...)
	}

	// --- index hit ratio (pg_statio_user_indexes) --------------------------------
	// SUM returns NULL when no user indexes exist; use *int64 pointers to detect this.
	// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STATIO-ALL-INDEXES-VIEW
	const idxSQL = "SELECT SUM(idx_blks_hit), SUM(idx_blks_read) FROM pg_statio_user_indexes"
	var idxHit, idxRead *int64
	if err := p.Conn.QueryRow(ctx, idxSQL).Scan(&idxHit, &idxRead); err != nil {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Index cache stats unavailable",
			Message: fmt.Sprintf("Could not query pg_statio_user_indexes for index-cache statistics: %v", err),
		})
	} else {
		out = append(out, evalIndexHitRatio(idxHit, idxRead)...)
	}

	// --- table hit ratio (pg_statio_user_tables) ---------------------------------
	// SUM returns NULL when no user tables exist; use *int64 pointers to detect this.
	// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STATIO-ALL-TABLES-VIEW
	const tblSQL = "SELECT SUM(heap_blks_hit), SUM(heap_blks_read) FROM pg_statio_user_tables"
	var heapHit, heapRead *int64
	if err := p.Conn.QueryRow(ctx, tblSQL).Scan(&heapHit, &heapRead); err != nil {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Table cache stats unavailable",
			Message: fmt.Sprintf("Could not query pg_statio_user_tables for table-cache statistics: %v", err),
		})
	} else {
		out = append(out, evalTableHitRatio(heapHit, heapRead)...)
	}

	// Version-check note: pg_stat_database, pg_statio_user_indexes, and
	// pg_statio_user_tables exist in all supported PG versions; no version
	// branching needed.

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.OK,
			Section: section,
			Title:   "Cache hit ratios look healthy",
			Message: fmt.Sprintf("Buffer cache hit ratio is ≥%.0f%%, index-block hit ratio is ≥%.0f%%, and table-block hit ratio is ≥%.0f%% (or no activity to measure). No cache-pressure concerns detected.", cacheHitRatioWarnThreshold*100, indexHitRatioInfoThreshold*100, tableHitRatioInfoThreshold*100),
			DocRef:  docPGStatDatabase,
		})
	}
	return out
}
