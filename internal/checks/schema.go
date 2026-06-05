package checks

import (
	"context"
	"fmt"
	"strings"

	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// docPGStatUserIndexes is the canonical reference for pg_stat_user_indexes.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-INDEXES-VIEW
const docPGStatUserIndexes = "https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-INDEXES-VIEW"

// docPGIndex is the canonical reference for the pg_index catalog.
// src: https://www.postgresql.org/docs/current/catalog-pg-index.html
const docPGIndex = "https://www.postgresql.org/docs/current/catalog-pg-index.html"

// docCreateIndexConcurrently is the canonical reference for CREATE INDEX CONCURRENTLY
// and the indisvalid/indisready columns.
// src: https://www.postgresql.org/docs/current/sql-createindex.html
const docCreateIndexConcurrently = "https://www.postgresql.org/docs/current/sql-createindex.html"

// docDDLConstraints is the canonical reference for table constraints, PKs and FKs.
// src: https://www.postgresql.org/docs/current/ddl-constraints.html
const docDDLConstraints = "https://www.postgresql.org/docs/current/ddl-constraints.html"

// docSchemaStatAllTables is the canonical reference for pg_stat_all_tables columns.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-TABLES-VIEW
const docSchemaStatAllTables = "https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-TABLES-VIEW"

// docObjectSizeFunctions is the canonical reference for database-object size functions.
// src: https://www.postgresql.org/docs/current/functions-admin.html#FUNCTIONS-ADMIN-DBOBJECT
const docObjectSizeFunctions = "https://www.postgresql.org/docs/current/functions-admin.html#FUNCTIONS-ADMIN-DBOBJECT"

// schemaSection is the section name emitted for all SchemaCheck advice items.
const schemaSection = "Schema"

// unusedIndexSizeThreshold is the minimum index size (1 MiB) below which we skip
// reporting a zero-scan index; tiny indexes are cheap to retain and may have been
// recently created.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-INDEXES-VIEW
const unusedIndexSizeThreshold int64 = 1 * 1024 * 1024 // 1 MiB

// noPKSizeThreshold is the minimum table size (1 MiB) above which we flag tables
// without a primary key.  Very small tables are often lookup/config tables where
// the omission is intentional.
// src: https://www.postgresql.org/docs/current/ddl-constraints.html
const noPKSizeThreshold int64 = 1 * 1024 * 1024 // 1 MiB

// bloatDeadRatioThreshold is the fraction of dead tuples vs total tuples above
// which we consider a table to have high bloat. 0.20 (20 %) is the default
// autovacuum scale factor; tables that autovacuum hasn't caught up with appear here.
// This is a simple heuristic from standard catalog columns, not pgstattuple.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-TABLES-VIEW
const bloatDeadRatioThreshold = 0.20

// bloatSizeThreshold is the minimum table size (1 MiB) below which we skip the
// bloat warning; bloat on tiny tables is inconsequential.
// src: https://www.postgresql.org/docs/current/functions-admin.html#FUNCTIONS-ADMIN-DBOBJECT
const bloatSizeThreshold int64 = 1 * 1024 * 1024 // 1 MiB

// neverAnalyzedLiveTupThreshold is the minimum n_live_tup count above which we flag
// a table that has never been analyzed or vacuumed.  Empty/tiny tables have no stats
// impact.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-TABLES-VIEW
const neverAnalyzedLiveTupThreshold int64 = 10_000

// schemaMessageCap is the maximum number of object names to include in a single
// advice message, to keep output readable.
const schemaMessageCap = 10

// largestRelationsN is the number of top-N largest user tables to report.
const largestRelationsN = 10

// ----- schema data types used by pure eval functions -------------------------

// UnusedIndexRow holds one row from the unused-index query.
type UnusedIndexRow struct {
	SchemaName string
	RelName    string
	IndexName  string
	SizeBytes  int64
}

// InvalidIndexRow holds one row from the invalid-index query.
type InvalidIndexRow struct {
	SchemaName string
	RelName    string
	IndexName  string
}

// DuplicateIndexGroup holds a group of indexes with identical definitions.
type DuplicateIndexGroup struct {
	RelName    string
	IndexNames []string // at least 2
}

// NoPKRow holds one row from the no-primary-key query.
type NoPKRow struct {
	SchemaName string
	RelName    string
	SizeBytes  int64
}

// FKNoIndexRow holds one row from the FK-without-index query.
type FKNoIndexRow struct {
	SchemaName string
	RelName    string
	ConstraintName string
}

// NeverAnalyzedRow holds one row from the never-analyzed query.
type NeverAnalyzedRow struct {
	SchemaName  string
	RelName     string
	LiveTuples  int64
	NeverVacuum bool // true when last_vacuum AND last_autovacuum are both NULL
}

// BloatRow holds one row from the bloat-estimate query.
type BloatRow struct {
	SchemaName string
	RelName    string
	DeadTuples int64
	LiveTuples int64
	SizeBytes  int64
}

// LargestRelRow holds one row from the largest-relations overview query.
type LargestRelRow struct {
	SchemaName string
	RelName    string
	TotalBytes int64
}

// ----- pure eval functions ---------------------------------------------------

// evalUnusedIndexes returns Info advice for non-unique user indexes with zero
// scans and size > threshold. Note: stats may be reset; message includes caveat.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-INDEXES-VIEW
// src: https://www.postgresql.org/docs/current/catalog-pg-index.html
func evalUnusedIndexes(rows []UnusedIndexRow) []pgtuner.Advice {
	if len(rows) == 0 {
		return nil
	}

	var names []string
	for _, r := range rows {
		names = append(names, fmt.Sprintf("%s.%s (%s, %.1f MiB)",
			r.SchemaName, r.IndexName, r.RelName, float64(r.SizeBytes)/(1024*1024)))
	}

	capped, suffix := capList(names, schemaMessageCap)
	return []pgtuner.Advice{{
		Level:   pgtuner.Info,
		Section: schemaSection,
		Title:   fmt.Sprintf("Unused indexes detected (%d)", len(rows)),
		Message: fmt.Sprintf("%d non-unique index(es) have idx_scan=0 and size > 1 MiB since the last statistics reset: %s%s. These are candidates for removal if the workload does not require them.",
			len(rows), strings.Join(capped, ", "), suffix),
		Detail: "Verify these indexes are not needed (e.g. recently created, or stats reset recently) then drop with DROP INDEX CONCURRENTLY. Check pg_stat_user_indexes.idx_scan for current counts.",
		DocRef: docPGStatUserIndexes,
	}}
}

// evalInvalidIndexes returns Warn advice for indexes that are not valid or not ready.
// Such indexes waste space and are never used by the query planner; they result from
// a failed CREATE INDEX CONCURRENTLY build.
// src: https://www.postgresql.org/docs/current/sql-createindex.html
// src: https://www.postgresql.org/docs/current/catalog-pg-index.html
func evalInvalidIndexes(rows []InvalidIndexRow) []pgtuner.Advice {
	if len(rows) == 0 {
		return nil
	}

	var names []string
	for _, r := range rows {
		names = append(names, fmt.Sprintf("%s.%s (on %s)", r.SchemaName, r.IndexName, r.RelName))
	}

	capped, suffix := capList(names, schemaMessageCap)
	return []pgtuner.Advice{{
		Level:   pgtuner.Warn,
		Section: schemaSection,
		Title:   fmt.Sprintf("Invalid index(es) detected (%d)", len(rows)),
		Message: fmt.Sprintf("%d index(es) have indisvalid=false or indisready=false in pg_index: %s%s. These indexes were not successfully built (typically from a failed CREATE INDEX CONCURRENTLY) and are not used by the query planner. They consume disk space and must be rebuilt.",
			len(rows), strings.Join(capped, ", "), suffix),
		Detail: "Drop the invalid index with DROP INDEX CONCURRENTLY and then recreate it: CREATE INDEX CONCURRENTLY ... Monitor pg_index.indisvalid after creation to confirm success.",
		DocRef: docCreateIndexConcurrently,
	}}
}

// evalDuplicateIndexes returns Info advice for groups of indexes on the same
// relation with identical (indrelid, indkey, indclass, indoption, indexprs, indpred).
// src: https://www.postgresql.org/docs/current/catalog-pg-index.html
func evalDuplicateIndexes(groups []DuplicateIndexGroup) []pgtuner.Advice {
	if len(groups) == 0 {
		return nil
	}

	var parts []string
	for _, g := range groups {
		parts = append(parts, fmt.Sprintf("%s: [%s]", g.RelName, strings.Join(g.IndexNames, ", ")))
	}

	capped, suffix := capList(parts, schemaMessageCap)
	return []pgtuner.Advice{{
		Level:   pgtuner.Info,
		Section: schemaSection,
		Title:   fmt.Sprintf("Duplicate indexes detected (%d group(s))", len(groups)),
		Message: fmt.Sprintf("%d group(s) of user indexes share identical definitions (same table, key columns, operator classes, options, expressions and predicate): %s%s. Redundant indexes slow down writes without benefiting queries.",
			len(groups), strings.Join(capped, "; "), suffix),
		Detail: "Keep the index whose name/constraint is most meaningful and drop the duplicates with DROP INDEX CONCURRENTLY.",
		DocRef: docPGIndex,
	}}
}

// evalNoPrimaryKey returns Info advice for user tables larger than the threshold
// that have no primary key. Tables without PKs cannot participate in logical
// replication, cause problems for tools like pg_upgrade, and slow deduplication.
// src: https://www.postgresql.org/docs/current/ddl-constraints.html
func evalNoPrimaryKey(rows []NoPKRow) []pgtuner.Advice {
	if len(rows) == 0 {
		return nil
	}

	var names []string
	for _, r := range rows {
		names = append(names, fmt.Sprintf("%s.%s (%.1f MiB)",
			r.SchemaName, r.RelName, float64(r.SizeBytes)/(1024*1024)))
	}

	capped, suffix := capList(names, schemaMessageCap)
	return []pgtuner.Advice{{
		Level:   pgtuner.Info,
		Section: schemaSection,
		Title:   fmt.Sprintf("Tables without a primary key (%d)", len(rows)),
		Message: fmt.Sprintf("%d user table(s) larger than 1 MiB have no primary key: %s%s. Tables without a primary key cannot be part of logical replication publications, and some maintenance/backup tools require or recommend primary keys.",
			len(rows), strings.Join(capped, ", "), suffix),
		Detail: "Add a primary key with ALTER TABLE ... ADD PRIMARY KEY (...). If a natural key is not available, use a generated serial/bigserial or UUID column.",
		DocRef: docDDLConstraints,
	}}
}

// evalFKWithoutIndex returns Info advice for foreign key constraints on user tables
// whose referencing columns are not covered by any user index. Missing FK indexes
// slow cascade deletes and UPDATE/DELETE on the referenced table (full sequential
// scan of the child table to find matching rows).
// src: https://www.postgresql.org/docs/current/ddl-constraints.html
// src: https://www.postgresql.org/docs/current/indexes-intro.html
func evalFKWithoutIndex(rows []FKNoIndexRow) []pgtuner.Advice {
	if len(rows) == 0 {
		return nil
	}

	var names []string
	for _, r := range rows {
		names = append(names, fmt.Sprintf("%s.%s (fk=%s)", r.SchemaName, r.RelName, r.ConstraintName))
	}

	capped, suffix := capList(names, schemaMessageCap)
	return []pgtuner.Advice{{
		Level:   pgtuner.Info,
		Section: schemaSection,
		Title:   fmt.Sprintf("Foreign key(s) without a supporting index (%d)", len(rows)),
		Message: fmt.Sprintf("%d foreign key constraint(s) have no index on the referencing column(s): %s%s. Without an index, PostgreSQL must scan the child table sequentially when deleting or updating rows in the referenced table, causing lock escalation and slow cascades.",
			len(rows), strings.Join(capped, ", "), suffix),
		Detail: "Create an index on the FK column(s): CREATE INDEX CONCURRENTLY ON child_table(fk_col). This speeds up cascade deletes, referential integrity checks, and JOIN lookups.",
		DocRef: docDDLConstraints,
	}}
}

// evalNeverAnalyzed returns Warn advice for user tables with n_live_tup above
// the threshold that have never been analyzed or autoanalyzed. The planner uses
// stale or default statistics for such tables, leading to poor plan choices.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-TABLES-VIEW
func evalNeverAnalyzed(rows []NeverAnalyzedRow) []pgtuner.Advice {
	if len(rows) == 0 {
		return nil
	}

	var names []string
	for _, r := range rows {
		entry := fmt.Sprintf("%s.%s (%d live tup)", r.SchemaName, r.RelName, r.LiveTuples)
		if r.NeverVacuum {
			entry += " [never vacuumed]"
		}
		names = append(names, entry)
	}

	capped, suffix := capList(names, schemaMessageCap)
	return []pgtuner.Advice{{
		Level:   pgtuner.Warn,
		Section: schemaSection,
		Title:   fmt.Sprintf("Tables never analyzed (stale planner stats) (%d)", len(rows)),
		Message: fmt.Sprintf("%d table(s) with more than %d live rows have never been analyzed (last_analyze and last_autoanalyze are both NULL in pg_stat_user_tables): %s%s. The query planner will use default statistics for these tables, which can result in poor query plans.",
			len(rows), neverAnalyzedLiveTupThreshold, strings.Join(capped, ", "), suffix),
		Detail: "Run ANALYZE on the listed tables, or ensure autovacuum/autoanalyze is enabled and not suppressed (autovacuum_enabled storage parameter). Check pg_stat_user_tables.last_autoanalyze.",
		DocRef: docSchemaStatAllTables,
	}}
}

// evalBloat returns Warn advice for user tables with a high ratio of dead tuples
// (n_dead_tup / (n_live_tup + n_dead_tup) > threshold) AND size above threshold.
// This is a simple heuristic using standard catalog stats columns; it is labelled as
// an estimate because it reflects the stats collector's counters, not actual on-disk
// page analysis.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-TABLES-VIEW
func evalBloat(rows []BloatRow) []pgtuner.Advice {
	var offenders []string
	for _, r := range rows {
		total := r.DeadTuples + r.LiveTuples
		if total <= 0 {
			continue
		}
		ratio := float64(r.DeadTuples) / float64(total)
		if ratio > bloatDeadRatioThreshold && r.SizeBytes > bloatSizeThreshold {
			offenders = append(offenders, fmt.Sprintf("%s.%s (dead=%.1f%%, size=%.1f MiB)",
				r.SchemaName, r.RelName,
				ratio*100, float64(r.SizeBytes)/(1024*1024)))
		}
	}

	if len(offenders) == 0 {
		return nil
	}

	capped, suffix := capList(offenders, schemaMessageCap)
	return []pgtuner.Advice{{
		Level:   pgtuner.Warn,
		Section: schemaSection,
		Title:   fmt.Sprintf("Table bloat estimate: high dead-tuple ratio (%d table(s))", len(offenders)),
		Message: fmt.Sprintf("Estimate (based on pg_stat_user_tables n_dead_tup/n_live_tup — not pgstattuple): %d table(s) have dead-tuple ratio > %.0f%% and size > 1 MiB: %s%s. Tables may benefit from VACUUM to reclaim space.",
			len(offenders), bloatDeadRatioThreshold*100, strings.Join(capped, ", "), suffix),
		Detail: "Run VACUUM on the listed tables or check that autovacuum is running. Use VACUUM FULL only during maintenance windows as it takes an exclusive lock. Dead-tuple ratio is an estimate; use pgstattuple extension for exact on-disk bloat measurement.",
		DocRef: docSchemaStatAllTables,
	}}
}

// evalLargestRelations returns an Info summary of the top-N user tables by total
// size (including TOAST and indexes). This is purely informational context.
// src: https://www.postgresql.org/docs/current/functions-admin.html#FUNCTIONS-ADMIN-DBOBJECT
func evalLargestRelations(rows []LargestRelRow) []pgtuner.Advice {
	if len(rows) == 0 {
		return nil
	}

	var parts []string
	for _, r := range rows {
		parts = append(parts, fmt.Sprintf("%s.%s (%.1f MiB)",
			r.SchemaName, r.RelName, float64(r.TotalBytes)/(1024*1024)))
	}

	return []pgtuner.Advice{{
		Level:   pgtuner.Info,
		Section: schemaSection,
		Title:   fmt.Sprintf("Largest user tables overview (top %d)", len(rows)),
		Message: fmt.Sprintf("Top %d user tables by pg_total_relation_size (includes indexes and TOAST): %s.",
			len(rows), strings.Join(parts, ", ")),
		Detail: "pg_total_relation_size includes the table heap, TOAST table, and all indexes. Use pg_relation_size for heap only.",
		DocRef: docObjectSizeFunctions,
	}}
}

// capList caps a string slice to maxN items, returning the (possibly shorter) slice
// and a suffix describing how many were omitted (empty when nothing is omitted).
func capList(items []string, maxN int) ([]string, string) {
	if len(items) <= maxN {
		return items, ""
	}
	return items[:maxN], fmt.Sprintf(" … and %d more", len(items)-maxN)
}

// ----- SchemaCheck -----------------------------------------------------------

// SchemaCheck evaluates schema-level health: unused/invalid/duplicate indexes,
// tables without PKs, foreign keys without supporting indexes, never-analyzed
// tables, table bloat, and largest-relations overview.
type SchemaCheck struct{ databaseScoped }

func (SchemaCheck) Name() string { return "schema" }

// Run executes all schema sub-checks.  Each sub-check is independent; a query
// failure produces a single Unknown-level advice for that sub-check while
// siblings continue.  It never panics and never returns an error.
func (SchemaCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	var out []pgtuner.Advice

	out = append(out, runUnusedIndexes(ctx, p)...)
	out = append(out, runInvalidIndexes(ctx, p)...)
	out = append(out, runDuplicateIndexes(ctx, p)...)
	out = append(out, runNoPrimaryKey(ctx, p)...)
	out = append(out, runFKWithoutIndex(ctx, p)...)
	out = append(out, runNeverAnalyzed(ctx, p)...)
	out = append(out, runBloat(ctx, p)...)
	out = append(out, runLargestRelations(ctx, p)...)

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.OK,
			Section: schemaSection,
			Title:   "Schema looks healthy",
			Message: "No unused, invalid or duplicate indexes detected; all user tables have primary keys; no FK constraints are missing supporting indexes; all tables have been analyzed; no significant bloat detected.",
			DocRef:  docPGStatUserIndexes,
		})
	}
	return out
}

// runUnusedIndexes queries pg_stat_user_indexes joined with pg_index to find
// user-created non-unique indexes that have never been scanned and are larger than
// the threshold.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-INDEXES-VIEW
// src: https://www.postgresql.org/docs/current/catalog-pg-index.html
func runUnusedIndexes(ctx context.Context, p *Probe) []pgtuner.Advice {
	// Join pg_stat_user_indexes with pg_index to exclude unique and PK constraints.
	// pg_relation_size(indexrelid) returns the on-disk size of the index file.
	// We exclude unique indexes (indisunique) and primary keys (indisprimary) as
	// those serve constraint enforcement roles beyond query performance.
	// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-INDEXES-VIEW
	// src: https://www.postgresql.org/docs/current/catalog-pg-index.html
	const sql = `
SELECT s.schemaname, s.relname, s.indexrelname,
       pg_relation_size(s.indexrelid) AS idx_size
FROM pg_stat_user_indexes s
JOIN pg_index ix ON ix.indexrelid = s.indexrelid
WHERE s.idx_scan = 0
  AND NOT ix.indisunique
  AND NOT ix.indisprimary
  AND pg_relation_size(s.indexrelid) > $1
ORDER BY pg_relation_size(s.indexrelid) DESC
LIMIT 20`

	rows, err := p.Conn.Query(ctx, sql, unusedIndexSizeThreshold)
	if err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "Unused index query failed",
			Message: fmt.Sprintf("Could not query pg_stat_user_indexes for unused indexes: %v", err),
			DocRef:  docPGStatUserIndexes,
		}}
	}
	defer rows.Close()

	var result []UnusedIndexRow
	for rows.Next() {
		var r UnusedIndexRow
		if err := rows.Scan(&r.SchemaName, &r.RelName, &r.IndexName, &r.SizeBytes); err != nil {
			return []pgtuner.Advice{{
				Level:   pgtuner.Unknown,
				Section: schemaSection,
				Title:   "Unused index scan failed",
				Message: fmt.Sprintf("Could not scan pg_stat_user_indexes row: %v", err),
				DocRef:  docPGStatUserIndexes,
			}}
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "Unused index iteration failed",
			Message: fmt.Sprintf("Error iterating pg_stat_user_indexes: %v", err),
			DocRef:  docPGStatUserIndexes,
		}}
	}

	return evalUnusedIndexes(result)
}

// runInvalidIndexes queries pg_index for entries where indisvalid=false or
// indisready=false, indicating a failed or incomplete index build.
// src: https://www.postgresql.org/docs/current/sql-createindex.html
// src: https://www.postgresql.org/docs/current/catalog-pg-index.html
func runInvalidIndexes(ctx context.Context, p *Probe) []pgtuner.Advice {
	// Join pg_index with pg_class (index) and pg_class (table) to get readable names.
	// Filter to user schemas (not pg_catalog or information_schema).
	// src: https://www.postgresql.org/docs/current/sql-createindex.html
	const sql = `
SELECT n.nspname AS schemaname, t.relname AS relname, i.relname AS indexname
FROM pg_index ix
JOIN pg_class i ON i.oid = ix.indexrelid
JOIN pg_class t ON t.oid = ix.indrelid
JOIN pg_namespace n ON n.oid = i.relnamespace
WHERE (NOT ix.indisvalid OR NOT ix.indisready)
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND t.relkind = 'r'
ORDER BY n.nspname, t.relname, i.relname
LIMIT 20`

	rows, err := p.Conn.Query(ctx, sql)
	if err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "Invalid index query failed",
			Message: fmt.Sprintf("Could not query pg_index for invalid indexes: %v", err),
			DocRef:  docCreateIndexConcurrently,
		}}
	}
	defer rows.Close()

	var result []InvalidIndexRow
	for rows.Next() {
		var r InvalidIndexRow
		if err := rows.Scan(&r.SchemaName, &r.RelName, &r.IndexName); err != nil {
			return []pgtuner.Advice{{
				Level:   pgtuner.Unknown,
				Section: schemaSection,
				Title:   "Invalid index scan failed",
				Message: fmt.Sprintf("Could not scan pg_index row for invalid indexes: %v", err),
				DocRef:  docCreateIndexConcurrently,
			}}
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "Invalid index iteration failed",
			Message: fmt.Sprintf("Error iterating pg_index for invalid indexes: %v", err),
			DocRef:  docCreateIndexConcurrently,
		}}
	}

	return evalInvalidIndexes(result)
}

// runDuplicateIndexes queries pg_index for user indexes that have identical
// (indrelid, indkey, indclass, indoption, indexprs, indpred) — meaning they
// index exactly the same columns with the same operator classes and predicates.
// Duplicate indexes waste disk and slow down writes.
// src: https://www.postgresql.org/docs/current/catalog-pg-index.html
func runDuplicateIndexes(ctx context.Context, p *Probe) []pgtuner.Advice {
	// Group by the full index definition signature.  We use COALESCE on the
	// nullable expression/predicate columns so that NULL compares equal to NULL
	// within a group.  pg_get_expr produces a stable textual representation.
	// We collect index names via array_agg so each group returns one row.
	// t.relname is included in the GROUP BY (not a dependency on indrelid alone,
	// since indrelid is not the primary key of pg_class in this context).
	// src: https://www.postgresql.org/docs/current/catalog-pg-index.html
	const sql = `
SELECT t.relname AS tablename,
       array_agg(i.relname ORDER BY i.relname) AS index_names
FROM pg_index ix
JOIN pg_class i ON i.oid = ix.indexrelid
JOIN pg_class t ON t.oid = ix.indrelid
JOIN pg_namespace n ON n.oid = i.relnamespace
WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND t.relkind = 'r'
GROUP BY ix.indrelid,
         t.relname,
         ix.indkey,
         ix.indclass,
         ix.indoption,
         COALESCE(pg_get_expr(ix.indexprs, ix.indrelid), ''),
         COALESCE(pg_get_expr(ix.indpred, ix.indrelid), '')
HAVING count(*) > 1
ORDER BY tablename
LIMIT 20`

	rows, err := p.Conn.Query(ctx, sql)
	if err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "Duplicate index query failed",
			Message: fmt.Sprintf("Could not query pg_index for duplicate indexes: %v", err),
			DocRef:  docPGIndex,
		}}
	}
	defer rows.Close()

	var groups []DuplicateIndexGroup
	for rows.Next() {
		var relName string
		var names []string
		if err := rows.Scan(&relName, &names); err != nil {
			return []pgtuner.Advice{{
				Level:   pgtuner.Unknown,
				Section: schemaSection,
				Title:   "Duplicate index scan failed",
				Message: fmt.Sprintf("Could not scan duplicate index group row: %v", err),
				DocRef:  docPGIndex,
			}}
		}
		groups = append(groups, DuplicateIndexGroup{RelName: relName, IndexNames: names})
	}
	if err := rows.Err(); err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "Duplicate index iteration failed",
			Message: fmt.Sprintf("Error iterating duplicate index groups: %v", err),
			DocRef:  docPGIndex,
		}}
	}

	return evalDuplicateIndexes(groups)
}

// runNoPrimaryKey queries pg_stat_user_tables and pg_class for user tables that
// have no primary key and are larger than the size threshold.
// src: https://www.postgresql.org/docs/current/ddl-constraints.html
// src: https://www.postgresql.org/docs/current/functions-admin.html#FUNCTIONS-ADMIN-DBOBJECT
func runNoPrimaryKey(ctx context.Context, p *Probe) []pgtuner.Advice {
	// Use pg_stat_user_tables to scope to user tables in the current database,
	// then check pg_index.indisprimary to determine whether a PK exists.
	// pg_relation_size scopes to the current database automatically.
	// src: https://www.postgresql.org/docs/current/ddl-constraints.html
	const sql = `
SELECT s.schemaname, s.relname, pg_relation_size(s.relid) AS tbl_size
FROM pg_stat_user_tables s
WHERE pg_relation_size(s.relid) > $1
  AND NOT EXISTS (
      SELECT 1 FROM pg_index ix
      WHERE ix.indrelid = s.relid
        AND ix.indisprimary
  )
ORDER BY tbl_size DESC
LIMIT 20`

	rows, err := p.Conn.Query(ctx, sql, noPKSizeThreshold)
	if err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "No-primary-key query failed",
			Message: fmt.Sprintf("Could not query pg_stat_user_tables for tables without primary keys: %v", err),
			DocRef:  docDDLConstraints,
		}}
	}
	defer rows.Close()

	var result []NoPKRow
	for rows.Next() {
		var r NoPKRow
		if err := rows.Scan(&r.SchemaName, &r.RelName, &r.SizeBytes); err != nil {
			return []pgtuner.Advice{{
				Level:   pgtuner.Unknown,
				Section: schemaSection,
				Title:   "No-primary-key scan failed",
				Message: fmt.Sprintf("Could not scan no-PK table row: %v", err),
				DocRef:  docDDLConstraints,
			}}
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "No-primary-key iteration failed",
			Message: fmt.Sprintf("Error iterating no-PK table rows: %v", err),
			DocRef:  docDDLConstraints,
		}}
	}

	return evalNoPrimaryKey(result)
}

// runFKWithoutIndex finds foreign key constraints on user tables whose referencing
// column set is not covered by any valid index on the child table.
//
// Detection method: for each FK constraint (contype='f') on a user table, check
// whether any valid index on the same table has indkey columns that contain all
// the FK referencing columns (conkey).  We use the @> array-containment operator
// on int2[] casts of indkey and conkey — this checks that every FK column appears
// somewhere in the index key list, regardless of order.  While prefix matching
// would be more precise for btree, containment is the common DBA heuristic and
// catches all practical cases without access-method branching.
//
// src: https://www.postgresql.org/docs/current/ddl-constraints.html
// src: https://www.postgresql.org/docs/current/catalog-pg-index.html
// src: https://www.postgresql.org/docs/current/catalog-pg-constraint.html
func runFKWithoutIndex(ctx context.Context, p *Probe) []pgtuner.Advice {
	// FK query: find FKs where NO valid index covers all FK referencing columns.
	// The @> operator on int2[] checks set containment (order-independent), which
	// is a defensible proxy for "index can be used for FK lookups".
	// We cast indkey (int2vector) → int2[] and conkey (int2[]) for the operator.
	// Only user tables (relkind='r', not pg_catalog/information_schema) are checked.
	// src: https://www.postgresql.org/docs/current/catalog-pg-constraint.html
	// src: https://www.postgresql.org/docs/current/catalog-pg-index.html
	const sql = `
SELECT n.nspname AS schemaname,
       t.relname AS relname,
       c.conname AS conname
FROM pg_constraint c
JOIN pg_class t ON t.oid = c.conrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
WHERE c.contype = 'f'
  AND t.relkind = 'r'
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND NOT EXISTS (
      SELECT 1
      FROM pg_index ix
      WHERE ix.indrelid = c.conrelid
        AND ix.indisvalid
        AND (ix.indkey::int2[]) @> c.conkey
  )
ORDER BY n.nspname, t.relname, c.conname
LIMIT 20`

	rows, err := p.Conn.Query(ctx, sql)
	if err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "FK-without-index query failed",
			Message: fmt.Sprintf("Could not query pg_constraint/pg_index for FK index coverage: %v", err),
			DocRef:  docDDLConstraints,
		}}
	}
	defer rows.Close()

	var result []FKNoIndexRow
	for rows.Next() {
		var r FKNoIndexRow
		if err := rows.Scan(&r.SchemaName, &r.RelName, &r.ConstraintName); err != nil {
			return []pgtuner.Advice{{
				Level:   pgtuner.Unknown,
				Section: schemaSection,
				Title:   "FK-without-index scan failed",
				Message: fmt.Sprintf("Could not scan FK-without-index row: %v", err),
				DocRef:  docDDLConstraints,
			}}
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "FK-without-index iteration failed",
			Message: fmt.Sprintf("Error iterating FK-without-index rows: %v", err),
			DocRef:  docDDLConstraints,
		}}
	}

	return evalFKWithoutIndex(result)
}

// runNeverAnalyzed queries pg_stat_user_tables for tables where both last_analyze
// and last_autoanalyze are NULL (never analyzed) with n_live_tup above threshold.
// Also flags tables where both last_vacuum and last_autovacuum are NULL.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-TABLES-VIEW
func runNeverAnalyzed(ctx context.Context, p *Probe) []pgtuner.Advice {
	// A table is "never analyzed" when both the manual and autovacuum/autoanalyze
	// timestamps are NULL.  We also capture whether it has never been vacuumed.
	// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-TABLES-VIEW
	const sql = `
SELECT schemaname, relname, n_live_tup,
       (last_vacuum IS NULL AND last_autovacuum IS NULL) AS never_vacuumed
FROM pg_stat_user_tables
WHERE last_analyze IS NULL
  AND last_autoanalyze IS NULL
  AND n_live_tup > $1
ORDER BY n_live_tup DESC
LIMIT 20`

	rows, err := p.Conn.Query(ctx, sql, neverAnalyzedLiveTupThreshold)
	if err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "Never-analyzed table query failed",
			Message: fmt.Sprintf("Could not query pg_stat_user_tables for never-analyzed tables: %v", err),
			DocRef:  docSchemaStatAllTables,
		}}
	}
	defer rows.Close()

	var result []NeverAnalyzedRow
	for rows.Next() {
		var r NeverAnalyzedRow
		if err := rows.Scan(&r.SchemaName, &r.RelName, &r.LiveTuples, &r.NeverVacuum); err != nil {
			return []pgtuner.Advice{{
				Level:   pgtuner.Unknown,
				Section: schemaSection,
				Title:   "Never-analyzed table scan failed",
				Message: fmt.Sprintf("Could not scan never-analyzed table row: %v", err),
				DocRef:  docSchemaStatAllTables,
			}}
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "Never-analyzed table iteration failed",
			Message: fmt.Sprintf("Error iterating never-analyzed table rows: %v", err),
			DocRef:  docSchemaStatAllTables,
		}}
	}

	return evalNeverAnalyzed(result)
}

// runBloat queries pg_stat_user_tables for tables whose estimated dead-tuple
// ratio (n_dead_tup / (n_live_tup + n_dead_tup)) exceeds the threshold and whose
// size exceeds the size threshold.  Results are ordered by dead-tuple count descending
// so the worst offenders appear first.
// This is an estimate from the stats collector counters, not an exact on-disk
// measurement.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-TABLES-VIEW
// src: https://www.postgresql.org/docs/current/functions-admin.html#FUNCTIONS-ADMIN-DBOBJECT
func runBloat(ctx context.Context, p *Probe) []pgtuner.Advice {
	const sql = `
SELECT schemaname, relname, n_dead_tup, n_live_tup,
       pg_relation_size(relid) AS tbl_size
FROM pg_stat_user_tables
WHERE (n_dead_tup + n_live_tup) > 0
  AND pg_relation_size(relid) > $1
ORDER BY n_dead_tup DESC
LIMIT 20`

	rows, err := p.Conn.Query(ctx, sql, bloatSizeThreshold)
	if err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "Table bloat query failed",
			Message: fmt.Sprintf("Could not query pg_stat_user_tables for bloat estimate: %v", err),
			DocRef:  docSchemaStatAllTables,
		}}
	}
	defer rows.Close()

	var result []BloatRow
	for rows.Next() {
		var r BloatRow
		if err := rows.Scan(&r.SchemaName, &r.RelName, &r.DeadTuples, &r.LiveTuples, &r.SizeBytes); err != nil {
			return []pgtuner.Advice{{
				Level:   pgtuner.Unknown,
				Section: schemaSection,
				Title:   "Table bloat scan failed",
				Message: fmt.Sprintf("Could not scan bloat table row: %v", err),
				DocRef:  docSchemaStatAllTables,
			}}
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "Table bloat iteration failed",
			Message: fmt.Sprintf("Error iterating bloat table rows: %v", err),
			DocRef:  docSchemaStatAllTables,
		}}
	}

	return evalBloat(result)
}

// runLargestRelations queries for the top-N user tables by total relation size
// (heap + TOAST + indexes) using pg_total_relation_size.  This is purely
// informational context.
// src: https://www.postgresql.org/docs/current/functions-admin.html#FUNCTIONS-ADMIN-DBOBJECT
func runLargestRelations(ctx context.Context, p *Probe) []pgtuner.Advice {
	const sql = `
SELECT s.schemaname, s.relname,
       pg_total_relation_size(s.relid) AS total_bytes
FROM pg_stat_user_tables s
ORDER BY total_bytes DESC
LIMIT $1`

	rows, err := p.Conn.Query(ctx, sql, largestRelationsN)
	if err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "Largest relations query failed",
			Message: fmt.Sprintf("Could not query pg_stat_user_tables for largest relations: %v", err),
			DocRef:  docObjectSizeFunctions,
		}}
	}
	defer rows.Close()

	var result []LargestRelRow
	for rows.Next() {
		var r LargestRelRow
		if err := rows.Scan(&r.SchemaName, &r.RelName, &r.TotalBytes); err != nil {
			return []pgtuner.Advice{{
				Level:   pgtuner.Unknown,
				Section: schemaSection,
				Title:   "Largest relations scan failed",
				Message: fmt.Sprintf("Could not scan largest relation row: %v", err),
				DocRef:  docObjectSizeFunctions,
			}}
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "Largest relations iteration failed",
			Message: fmt.Sprintf("Error iterating largest relation rows: %v", err),
			DocRef:  docObjectSizeFunctions,
		}}
	}

	return evalLargestRelations(result)
}
