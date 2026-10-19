package checks

import (
	"context"
	"fmt"

	"github.com/soylic/pgtuner/pkg/pgtuner"
)

const docPGStatDatabase = "https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-DATABASE-VIEW"

const docPGStatioUserIndexes = "https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STATIO-ALL-INDEXES-VIEW"

const docPGStatioUserTables = "https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STATIO-ALL-TABLES-VIEW"

// cacheHitRatioWarnThreshold: below 99 %, more than 1 % of block requests go to
// disk, which suggests shared_buffers may be undersized for the working set.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-DATABASE-VIEW
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-SHARED-BUFFERS
const cacheHitRatioWarnThreshold = 0.99 // 99 %

// indexHitRatioInfoThreshold: below 95 % is a soft sign that shared_buffers
// does not retain frequently used index pages.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STATIO-ALL-INDEXES-VIEW
const indexHitRatioInfoThreshold = 0.95 // 95 %

// tableHitRatioInfoThreshold: below 95 % is a soft sign that frequently accessed
// tables do not stay in shared_buffers.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STATIO-ALL-TABLES-VIEW
const tableHitRatioInfoThreshold = 0.95 // 95 %

// CacheCheck evaluates shared-buffer and index-block cache hit ratios.
type CacheCheck struct{ databaseScoped }

func (CacheCheck) Name() string { return "cache" }

// evalCacheHitRatio turns blks_hit and blks_read from pg_stat_database into advice.
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
			Message: fmt.Sprintf("Buffer cache hit ratio is %.2f%% (blks_hit=%d, blks_read=%d). Requests served from disk instead of shared_buffers exceed 1%%. This may indicate that shared_buffers is undersized for the working set, or that the working set is too large to cache.", ratio*100, blksHit, blksRead),
			Detail:  "Consider increasing shared_buffers (commonly 25–40% of RAM for a dedicated PG host). Or review query access patterns to reduce the effective working set.",
			DocRef:  docPGStatDatabase,
		}}
	}
	return nil
}

// evalIndexHitRatio takes SUM(idx_blks_hit) and SUM(idx_blks_read) from
// pg_statio_user_indexes. The inputs are pointers because SUM is NULL when no
// user indexes exist, which yields no advice.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STATIO-ALL-INDEXES-VIEW
func evalIndexHitRatio(idxHit, idxRead *int64) []pgtuner.Advice {
	const section = "Cache"

	if idxHit == nil || idxRead == nil {
		return nil
	}

	total := *idxHit + *idxRead
	if total == 0 {
		// Indexes exist but nothing accessed them since the stats reset.
		return nil
	}

	ratio := float64(*idxHit) / float64(total)
	if ratio < indexHitRatioInfoThreshold {
		return []pgtuner.Advice{{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "Index block cache hit ratio is low",
			Message: fmt.Sprintf("Index-block cache hit ratio is %.2f%% (idx_blks_hit=%d, idx_blks_read=%d). Disk serves more than 5%% of index-page requests. Frequently used index pages may not stay in shared_buffers.", ratio*100, *idxHit, *idxRead),
			Detail:  "Increasing shared_buffers allows more index pages to stay in cache. Consider also checking pg_stat_user_indexes to see which indexes have heavy access.",
			DocRef:  docPGStatioUserIndexes,
		}}
	}
	return nil
}

// evalTableHitRatio takes SUM(heap_blks_hit) and SUM(heap_blks_read) from
// pg_statio_user_tables. The inputs are pointers because SUM is NULL when no
// user tables exist, which yields no advice.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STATIO-ALL-TABLES-VIEW
func evalTableHitRatio(heapHit, heapRead *int64) []pgtuner.Advice {
	const section = "Cache"

	if heapHit == nil || heapRead == nil {
		return nil
	}

	total := *heapHit + *heapRead
	if total == 0 {
		// Tables exist but nothing accessed them since the stats reset.
		return nil
	}

	ratio := float64(*heapHit) / float64(total)
	if ratio < tableHitRatioInfoThreshold {
		return []pgtuner.Advice{{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "Table block cache hit ratio is low",
			Message: fmt.Sprintf("Table cache hit ratio is %.2f%% (heap_blks_hit=%d, heap_blks_read=%d). Disk serves more than 5%% of table-page requests. Frequently accessed tables may not stay in shared_buffers.", ratio*100, *heapHit, *heapRead),
			Detail:  "Increasing shared_buffers allows more table pages to stay in cache. Consider also checking pg_statio_user_tables to see which tables have heavy access.",
			DocRef:  docPGStatioUserTables,
		}}
	}
	return nil
}

// Run queries the three hit ratios. A query or scan failure yields one Unknown
// advice for that sub-check and the others still run.
// It never panics and never returns an error.
func (CacheCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "Cache"
	var out []pgtuner.Advice

	// blks_hit: requests served from shared_buffers. blks_read: requests that needed a physical read.
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

	// SUM returns NULL when no user indexes exist, hence the pointers.
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

	// SUM returns NULL when no user tables exist, hence the pointers.
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
