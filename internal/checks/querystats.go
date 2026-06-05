package checks

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/OpenRemedy/pgtuner/internal/pg"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// docQueryStatStatements is the canonical reference for the pg_stat_statements view
// and the extension that populates it.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
const docQueryStatStatements = "https://www.postgresql.org/docs/current/pgstatstatements.html"

// queryStatsSection is the section name emitted for all QueryStatsCheck advice items.
const queryStatsSection = "Query Performance"

// queryStatsFetchLimit is the number of rows fetched from pg_stat_statements in a
// single round-trip.  All three sub-views (top-time, low-cache-hit, temp-heavy)
// are derived from this in-memory result set.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
const queryStatsFetchLimit = 50

// queryStatsTopN is the maximum number of queries to surface in each sub-view
// advice message.
const queryStatsTopN = 5

// queryStatsQueryTruncLen is the maximum rune-length of a normalized query text
// included in an advice message.  Queries longer than this are trimmed and
// suffixed with "…" for readability.
const queryStatsQueryTruncLen = 100

// cacheHitRatioThreshold is the minimum acceptable ratio of
// shared_blks_hit / (shared_blks_hit + shared_blks_read) below which we emit
// a low-cache-hit advice.  Below 0.90 (90 %) a query is spending a significant
// fraction of its I/O on physical reads rather than the shared buffer pool.
// The 90 % threshold is derived from the documented purpose of shared_buffers:
// the buffer pool is intended to serve the "hot" working set; a query with a
// hit rate below 90 % indicates its working set exceeds the buffer pool or the
// table is not frequently accessed.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-SHARED-BUFFERS
const cacheHitRatioThreshold = 0.90

// cacheHitMinTotalBlocks is the minimum total block accesses
// (shared_blks_hit + shared_blks_read) a query must have made for the cache-hit
// ratio to be meaningful.  Queries that touch very few blocks have a noisy ratio.
// 1000 blocks (~8 MiB at 8 kB/block) is a reasonable signal floor.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
const cacheHitMinTotalBlocks = 1000

// tempBlksWrittenThreshold is the minimum temp_blks_written count above which
// we surface a query as temp-heavy.  A query that has never spilled to disk has
// temp_blks_written=0.  Any positive value indicates at least one sort/hash
// spill; thresholds of 1 block (8 kB) are too noisy.  We use 128 blocks (~1 MiB)
// as the minimum that indicates a meaningful spill worth noting.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-WORK-MEM
const tempBlksWrittenThreshold = 128

// whitespaceRe collapses runs of whitespace (including newlines and tabs) to a
// single space when producing truncated query text for advice messages.
var whitespaceRe = regexp.MustCompile(`\s+`)

// ----- version-branched column helper -----------------------------------------

// queryStatsColumns returns the correct column names for the pg_stat_statements
// total execution time and mean execution time columns based on the server
// version.  The columns were renamed in PostgreSQL 13:
//
//   - PG ≤ 12: total_time, mean_time
//   - PG ≥ 13: total_exec_time, mean_exec_time
//
// Verified live against postgres:12-alpine and postgres:13-alpine containers.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
// src: https://www.postgresql.org/docs/13/pgstatstatements.html (PG13 release notes)
func queryStatsColumns(v pg.ServerVersion) (totalCol, meanCol string) {
	if v.AtLeast(13, 0) {
		return "total_exec_time", "mean_exec_time"
	}
	return "total_time", "mean_time"
}

// ----- row type ---------------------------------------------------------------

// queryStatsRow holds one row from the pg_stat_statements query.
// All fields needed by all three sub-views are collected in a single round-trip.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
type queryStatsRow struct {
	Query           string  // normalized query text
	Calls           int64   // number of times the statement was executed
	TotalExecMs     float64 // total execution time in milliseconds
	MeanExecMs      float64 // mean execution time per call in milliseconds
	Rows            int64   // total rows returned / affected
	SharedBlksHit   int64   // buffer pool hits
	SharedBlksRead  int64   // disk (OS cache) reads
	TempBlksWritten int64   // blocks written to temporary files (sort/hash spills)
}

// ----- pure eval functions ----------------------------------------------------

// truncateQuery collapses whitespace and truncates the normalized query text to
// queryStatsQueryTruncLen runes for inclusion in advice messages.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html (query normalization)
func truncateQuery(q string) string {
	q = whitespaceRe.ReplaceAllString(strings.TrimSpace(q), " ")
	runes := []rune(q)
	if len(runes) > queryStatsQueryTruncLen {
		return string(runes[:queryStatsQueryTruncLen]) + "…"
	}
	return q
}

// evalTopQueryByTime returns an Info advice listing the top queries by total
// execution time.  It is called only when rows is non-empty.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
func evalTopQueryByTime(rows []queryStatsRow) []pgtuner.Advice {
	if len(rows) == 0 {
		return nil
	}

	// Rows are already ordered by total_exec_time DESC from the query.
	n := len(rows)
	if n > queryStatsTopN {
		n = queryStatsTopN
	}

	var parts []string
	for _, r := range rows[:n] {
		parts = append(parts, fmt.Sprintf(
			"[%.0f ms total, %d calls, %.2f ms/call] %s",
			r.TotalExecMs, r.Calls, r.MeanExecMs, truncateQuery(r.Query),
		))
	}

	return []pgtuner.Advice{{
		Level:   pgtuner.Info,
		Section: queryStatsSection,
		Title:   fmt.Sprintf("Top %d queries by total execution time (pg_stat_statements)", n),
		Message: fmt.Sprintf(
			"The following %d queries account for the most cumulative execution time since the last statistics reset. "+
				"Review them for optimization opportunities (indexing, query rewriting, caching).\n%s",
			n, strings.Join(parts, "\n")),
		Detail: "Statistics are cumulative since the last pg_stat_statements_reset() call. " +
			"Use EXPLAIN (ANALYZE, BUFFERS) to investigate individual query plans. " +
			"Consider calling pg_stat_statements_reset() after tuning to get a fresh baseline.",
		DocRef: docQueryStatStatements,
	}}
}

// evalLowCacheHitQueries returns an Info advice for queries whose shared buffer
// hit ratio is below the threshold and whose total block accesses are significant
// enough to be meaningful.
//
// Hit ratio = shared_blks_hit / (shared_blks_hit + shared_blks_read).
// A ratio below 0.90 means > 10 % of block accesses required a physical read.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
func evalLowCacheHitQueries(rows []queryStatsRow) []pgtuner.Advice {
	type offender struct {
		row      queryStatsRow
		hitRatio float64
		total    int64
	}

	var offenders []offender
	for _, r := range rows {
		total := r.SharedBlksHit + r.SharedBlksRead
		if total < cacheHitMinTotalBlocks {
			continue
		}
		ratio := float64(r.SharedBlksHit) / float64(total)
		if ratio >= cacheHitRatioThreshold {
			continue
		}
		offenders = append(offenders, offender{row: r, hitRatio: ratio, total: total})
	}

	if len(offenders) == 0 {
		return nil
	}

	n := len(offenders)
	if n > queryStatsTopN {
		n = queryStatsTopN
	}

	var parts []string
	for _, o := range offenders[:n] {
		parts = append(parts, fmt.Sprintf(
			"[hit=%.1f%%, %d total blks, %d calls] %s",
			o.hitRatio*100, o.total, o.row.Calls, truncateQuery(o.row.Query),
		))
	}

	return []pgtuner.Advice{{
		Level:   pgtuner.Info,
		Section: queryStatsSection,
		Title:   fmt.Sprintf("Queries with low shared-buffer hit ratio (%d)", len(offenders)),
		Message: fmt.Sprintf(
			"%d query/queries have a shared-buffer cache-hit ratio below %.0f%% with > %d total block accesses. "+
				"These queries are performing significant physical disk reads. "+
				"Top offenders:\n%s",
			len(offenders), cacheHitRatioThreshold*100, cacheHitMinTotalBlocks,
			strings.Join(parts, "\n")),
		Detail: "Increasing shared_buffers or work_mem may help if the working set fits. " +
			"Also consider adding indexes to reduce the number of block accesses per query. " +
			"Enable track_io_timing to measure actual I/O latency.",
		DocRef: docQueryStatStatements,
	}}
}

// evalTempHeavyQueries returns an Info advice for queries that have written
// significant amounts of data to temporary files (sort/hash spills).
// temp_blks_written > tempBlksWrittenThreshold indicates the query's sort or
// hash operation exceeded work_mem and spilled to disk.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-WORK-MEM
func evalTempHeavyQueries(rows []queryStatsRow) []pgtuner.Advice {
	type offender struct {
		row queryStatsRow
	}

	var offenders []offender
	for _, r := range rows {
		if r.TempBlksWritten >= tempBlksWrittenThreshold {
			offenders = append(offenders, offender{row: r})
		}
	}

	if len(offenders) == 0 {
		return nil
	}

	n := len(offenders)
	if n > queryStatsTopN {
		n = queryStatsTopN
	}

	var parts []string
	for _, o := range offenders[:n] {
		parts = append(parts, fmt.Sprintf(
			"[%d temp blks written (~%.1f MiB), %d calls] %s",
			o.row.TempBlksWritten,
			float64(o.row.TempBlksWritten)*8.0/1024.0, // 8 kB per block → MiB
			o.row.Calls,
			truncateQuery(o.row.Query),
		))
	}

	return []pgtuner.Advice{{
		Level:   pgtuner.Info,
		Section: queryStatsSection,
		Title:   fmt.Sprintf("Queries with high temp-file usage (%d)", len(offenders)),
		Message: fmt.Sprintf(
			"%d query/queries have written >= %d blocks (~%.1f MiB) to temporary files "+
				"(sort or hash spills that exceeded work_mem). "+
				"Top offenders:\n%s",
			len(offenders), tempBlksWrittenThreshold,
			float64(tempBlksWrittenThreshold)*8.0/1024.0,
			strings.Join(parts, "\n")),
		Detail: "Increasing work_mem can eliminate spills for in-memory sorts and hash joins, " +
			"but note that work_mem is per-sort-node per-session — large values multiply across connections. " +
			"Consider rewriting queries with large sorts (e.g. add indexes, use CTEs, limit result sets). " +
			"Use EXPLAIN (ANALYZE, BUFFERS) to identify which plan nodes are spilling.",
		DocRef: docQueryStatStatements,
	}}
}

// ----- QueryStatsCheck --------------------------------------------------------

// QueryStatsCheck evaluates query-level performance data from the
// pg_stat_statements extension.  When the extension is not installed it emits
// a single informational advice and returns; when it is present it queries the
// view once and derives three sub-views in memory:
//
//  1. Top queries by total execution time (top 5, Info).
//  2. Queries with a low shared-buffer cache-hit ratio (Info).
//  3. Queries with high temp-file writes indicating work_mem pressure (Info).
//
// All sub-checks are isolated; a query failure for the pg_stat_statements data
// returns a single Unknown-level advice while other checks continue.
// It never panics and never returns an error.
type QueryStatsCheck struct{ databaseScoped }

func (QueryStatsCheck) Name() string { return "querystats" }

// Run executes the query-performance analysis.  It first detects whether
// pg_stat_statements is installed; if not, it emits a single pointed Info and
// returns without duplicating the shared_preload_libraries advice already
// emitted by StatisticsCheck.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
func (QueryStatsCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	// --- Detect whether the extension is installed ----------------------------
	// Query pg_extension, which is the authoritative catalog for installed
	// extensions in the current database.
	// src: https://www.postgresql.org/docs/current/catalog-pg-extension.html
	const detectSQL = `SELECT 1 FROM pg_extension WHERE extname = 'pg_stat_statements'`
	var present int
	err := p.Conn.QueryRow(ctx, detectSQL).Scan(&present)
	if err != nil {
		// pgx returns pgx.ErrNoRows when the SELECT returns no rows; treat that
		// as "extension absent".  Any other error is a genuine query failure.
		if !isNoRows(err) {
			return []pgtuner.Advice{{
				Level:   pgtuner.Unknown,
				Section: queryStatsSection,
				Title:   "pg_stat_statements detection query failed",
				Message: fmt.Sprintf("Could not query pg_extension to detect pg_stat_statements: %v", err),
				DocRef:  docQueryStatStatements,
			}}
		}
		// err == pgx.ErrNoRows → extension absent.
		present = 0
	}

	if present == 0 {
		return []pgtuner.Advice{{
			Level:   pgtuner.Info,
			Section: queryStatsSection,
			Title:   "pg_stat_statements is not installed",
			Message: "pg_stat_statements is not installed in this database. " +
				"Install it to enable per-query performance analysis (top queries by time, cache-hit rates, temp-file spills). " +
				"See the Statistics section for guidance on enabling it in shared_preload_libraries.",
			DocRef: docQueryStatStatements,
		}}
	}

	// --- Fetch pg_stat_statements in a single round-trip ----------------------
	rows, fetchErr := fetchQueryStats(ctx, p)
	if fetchErr != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: queryStatsSection,
			Title:   "pg_stat_statements query failed",
			Message: fmt.Sprintf("Could not query pg_stat_statements: %v", fetchErr),
			DocRef:  docQueryStatStatements,
		}}
	}

	// --- Derive sub-views from the in-memory rows ----------------------------
	var out []pgtuner.Advice
	out = append(out, evalTopQueryByTime(rows)...)
	out = append(out, evalLowCacheHitQueries(rows)...)
	out = append(out, evalTempHeavyQueries(rows)...)

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.OK,
			Section: queryStatsSection,
			Title:   "Query performance looks healthy",
			Message: "pg_stat_statements is installed; no significant concerns detected " +
				"(no high-time queries, low-cache-hit queries, or temp-heavy queries found in the top 50 by total execution time).",
			DocRef: docQueryStatStatements,
		})
	}
	return out
}

// fetchQueryStats issues a single query against pg_stat_statements and returns
// up to queryStatsFetchLimit rows ordered by total execution time descending.
// The correct column names for the server version are selected via
// queryStatsColumns.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
func fetchQueryStats(ctx context.Context, p *Probe) ([]queryStatsRow, error) {
	totalCol, meanCol := queryStatsColumns(p.Version)

	// We SELECT only the columns we consume, ordered by total execution time so
	// that evalTopQueryByTime can use rows[:N] directly.  All three sub-views
	// operate over the same row set.
	// columns: query, calls, <totalCol>, <meanCol>, rows, shared_blks_hit,
	//          shared_blks_read, temp_blks_written
	// src: https://www.postgresql.org/docs/current/pgstatstatements.html
	sql := fmt.Sprintf(
		`SELECT query, calls, %s, %s, rows, shared_blks_hit, shared_blks_read, temp_blks_written
		 FROM pg_stat_statements
		 ORDER BY %s DESC
		 LIMIT %d`,
		totalCol, meanCol, totalCol, queryStatsFetchLimit,
	)

	pgRows, err := p.Conn.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer pgRows.Close()

	var result []queryStatsRow
	for pgRows.Next() {
		var r queryStatsRow
		if err := pgRows.Scan(
			&r.Query,
			&r.Calls,
			&r.TotalExecMs,
			&r.MeanExecMs,
			&r.Rows,
			&r.SharedBlksHit,
			&r.SharedBlksRead,
			&r.TempBlksWritten,
		); err != nil {
			return nil, fmt.Errorf("scan pg_stat_statements row: %w", err)
		}
		result = append(result, r)
	}
	if err := pgRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pg_stat_statements: %w", err)
	}

	return result, nil
}

// isNoRows returns true when err is the pgx no-rows sentinel.
// We use a string match because importing pgx in the checks package just for
// the sentinel constant would add a direct pgx dependency to the checks layer;
// the error message is stable.
func isNoRows(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "no rows in result set")
}
