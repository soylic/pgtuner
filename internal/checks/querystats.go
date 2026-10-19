package checks

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/soylic/pgtuner/internal/pg"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// src: https://www.postgresql.org/docs/current/pgstatstatements.html
const docQueryStatStatements = "https://www.postgresql.org/docs/current/pgstatstatements.html"

const queryStatsSection = "Query Performance"

// queryStatsFetchLimit is the row count fetched once from pg_stat_statements.
// All three sub-views derive from this result set.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
const queryStatsFetchLimit = 50

// queryStatsTopN is the maximum number of queries listed per sub-view.
const queryStatsTopN = 5

// queryStatsQueryTruncLen is the maximum rune length of query text in a message.
// Longer text is trimmed and gets a "…" suffix.
const queryStatsQueryTruncLen = 100

// cacheHitRatioThreshold is the minimum shared_blks_hit / (shared_blks_hit + shared_blks_read).
// Below 90 % the query working set probably exceeds the buffer pool, or the table is rarely accessed.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-SHARED-BUFFERS
const cacheHitRatioThreshold = 0.90

// cacheHitMinTotalBlocks is the minimum shared_blks_hit + shared_blks_read for a meaningful ratio.
// Queries that touch few blocks have a noisy ratio. 1000 blocks is about 8 MiB.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
const cacheHitMinTotalBlocks = 1000

// tempBlksWrittenThreshold is the minimum temp_blks_written that marks a query as temp-heavy.
// A threshold of 1 block (8 kB) is too noisy. 128 blocks is about 1 MiB.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-WORK-MEM
const tempBlksWrittenThreshold = 128

// whitespaceRe collapses whitespace runs in query text.
var whitespaceRe = regexp.MustCompile(`\s+`)

// queryStatsColumns returns the total and mean time column names.
// PostgreSQL 13 renamed them: PG 12 and earlier use total_time and mean_time, PG 13 and later use total_exec_time and mean_exec_time.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
// src: https://www.postgresql.org/docs/13/pgstatstatements.html (PG13 release notes)
func queryStatsColumns(v pg.ServerVersion) (totalCol, meanCol string) {
	if v.AtLeast(13, 0) {
		return "total_exec_time", "mean_exec_time"
	}
	return "total_time", "mean_time"
}

// queryStatsRow holds one row from the pg_stat_statements query.
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

// truncateQuery collapses whitespace and cuts query text to queryStatsQueryTruncLen runes.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html (query normalization)
func truncateQuery(q string) string {
	q = whitespaceRe.ReplaceAllString(strings.TrimSpace(q), " ")
	runes := []rune(q)
	if len(runes) > queryStatsQueryTruncLen {
		return string(runes[:queryStatsQueryTruncLen]) + "…"
	}
	return q
}

// evalTopQueryByTime returns an Info advice that lists the top queries by total execution time.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
func evalTopQueryByTime(rows []queryStatsRow) []pgtuner.Advice {
	if len(rows) == 0 {
		return nil
	}

	// The query already orders rows by total time descending.
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
			"These %d queries used the most cumulative execution time since the last statistics reset. "+
				"Review them for ways to improve them (indexing, query rewriting, caching).\n%s",
			n, strings.Join(parts, "\n")),
		Detail: "Statistics are cumulative since the last pg_stat_statements_reset() call. " +
			"Use EXPLAIN (ANALYZE, BUFFERS) to examine individual query plans. " +
			"Consider calling pg_stat_statements_reset() after tuning to start a new baseline.",
		DocRef: docQueryStatStatements,
	}}
}

// evalLowCacheHitQueries returns an Info advice for queries with a low shared-buffer hit ratio.
// A ratio below 0.90 means more than 10 % of block accesses needed a physical read.
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
				"These queries do many physical disk reads. "+
				"Top offenders:\n%s",
			len(offenders), cacheHitRatioThreshold*100, cacheHitMinTotalBlocks,
			strings.Join(parts, "\n")),
		Detail: "A higher shared_buffers or work_mem may help if the working set fits. " +
			"Consider also indexes, to reduce the number of block accesses per query. " +
			"Enable track_io_timing to measure the real I/O latency.",
		DocRef: docQueryStatStatements,
	}}
}

// evalTempHeavyQueries returns an Info advice for queries that wrote much data to temporary files.
// Such writes mean a sort or hash exceeded work_mem and spilled to disk.
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
			float64(o.row.TempBlksWritten)*8.0/1024.0, // 8 kB per block to MiB
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
		Detail: "A higher work_mem can remove spills for in-memory sorts and hash joins. " +
			"work_mem applies per sort node per session, so large values multiply across connections. " +
			"Consider rewriting queries that have large sorts (for example add indexes, use CTEs, limit result sets). " +
			"Use EXPLAIN (ANALYZE, BUFFERS) to find the plan nodes that spill.",
		DocRef: docQueryStatStatements,
	}}
}

// QueryStatsCheck evaluates pg_stat_statements data. It queries the view once and derives three Info sub-views:
// top queries by total time, low cache-hit queries, and temp-heavy queries.
// A missing extension gives one Info. A query failure gives one Unknown.
type QueryStatsCheck struct{ databaseScoped }

func (QueryStatsCheck) Name() string { return "querystats" }

// Run detects pg_stat_statements first. When it is absent, Run does not repeat the shared_preload_libraries advice that StatisticsCheck emits.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
func (QueryStatsCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	// src: https://www.postgresql.org/docs/current/catalog-pg-extension.html
	const detectSQL = `SELECT 1 FROM pg_extension WHERE extname = 'pg_stat_statements'`
	var present int
	err := p.Conn.QueryRow(ctx, detectSQL).Scan(&present)
	if err != nil {
		// No rows means the extension is absent. Any other error is a query failure.
		if !isNoRows(err) {
			return []pgtuner.Advice{{
				Level:   pgtuner.Unknown,
				Section: queryStatsSection,
				Title:   "pg_stat_statements detection query failed",
				Message: fmt.Sprintf("pgtuner could not query pg_extension to detect pg_stat_statements: %v", err),
				DocRef:  docQueryStatStatements,
			}}
		}
		present = 0
	}

	if present == 0 {
		return []pgtuner.Advice{{
			Level:   pgtuner.Info,
			Section: queryStatsSection,
			Title:   "pg_stat_statements is not installed",
			Message: "pg_stat_statements is not installed in this database. " +
				"Install it to enable per-query performance analysis (top queries by time, cache-hit rates, temp-file spills). " +
				"The Statistics section explains how to enable it in shared_preload_libraries.",
			DocRef: docQueryStatStatements,
		}}
	}

	rows, fetchErr := fetchQueryStats(ctx, p)
	if fetchErr != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: queryStatsSection,
			Title:   "pg_stat_statements query failed",
			Message: fmt.Sprintf("pgtuner could not query pg_stat_statements: %v", fetchErr),
			DocRef:  docQueryStatStatements,
		}}
	}

	var out []pgtuner.Advice
	out = append(out, evalTopQueryByTime(rows)...)
	out = append(out, evalLowCacheHitQueries(rows)...)
	out = append(out, evalTempHeavyQueries(rows)...)

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.OK,
			Section: queryStatsSection,
			Title:   "Query performance looks healthy",
			Message: "pg_stat_statements is installed. pgtuner found no significant concerns " +
				"(no high-time queries, low-cache-hit queries, or temp-heavy queries in the top 50 by total execution time).",
			DocRef: docQueryStatStatements,
		})
	}
	return out
}

// fetchQueryStats returns up to queryStatsFetchLimit rows ordered by total execution time descending.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
func fetchQueryStats(ctx context.Context, p *Probe) ([]queryStatsRow, error) {
	totalCol, meanCol := queryStatsColumns(p.Version)

	// Ordering by total time lets evalTopQueryByTime use rows[:N] directly.
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

// isNoRows matches the pgx no-rows error by message to avoid a direct pgx dependency in this package.
func isNoRows(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "no rows in result set")
}
