package checks

import (
	"context"
	"fmt"
	"strings"

	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-INDEXES-VIEW
const docPGStatUserIndexes = "https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-INDEXES-VIEW"

// src: https://www.postgresql.org/docs/current/catalog-pg-index.html
const docPGIndex = "https://www.postgresql.org/docs/current/catalog-pg-index.html"

// docCreateIndexConcurrently documents CREATE INDEX CONCURRENTLY and the indisvalid/indisready columns.
// src: https://www.postgresql.org/docs/current/sql-createindex.html
const docCreateIndexConcurrently = "https://www.postgresql.org/docs/current/sql-createindex.html"

// src: https://www.postgresql.org/docs/current/ddl-constraints.html
const docDDLConstraints = "https://www.postgresql.org/docs/current/ddl-constraints.html"

// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-TABLES-VIEW
const docSchemaStatAllTables = "https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-TABLES-VIEW"

// src: https://www.postgresql.org/docs/current/functions-admin.html#FUNCTIONS-ADMIN-DBOBJECT
const docObjectSizeFunctions = "https://www.postgresql.org/docs/current/functions-admin.html#FUNCTIONS-ADMIN-DBOBJECT"

const schemaSection = "Schema"

// unusedIndexSizeThreshold is the minimum size (1 MiB) for reporting a zero-scan index.
// Tiny indexes are cheap to keep and may be new.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-INDEXES-VIEW
const unusedIndexSizeThreshold int64 = 1 * 1024 * 1024 // 1 MiB

// noPKSizeThreshold is the minimum table size (1 MiB) for flagging a missing primary key.
// Very small lookup or config tables often omit it on purpose.
// src: https://www.postgresql.org/docs/current/ddl-constraints.html
const noPKSizeThreshold int64 = 1 * 1024 * 1024 // 1 MiB

// bloatDeadRatioThreshold is the dead-tuple fraction above which a table has high bloat.
// 0.20 equals the default autovacuum scale factor. This is a heuristic from catalog columns, not pgstattuple.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-TABLES-VIEW
const bloatDeadRatioThreshold = 0.20

// bloatSizeThreshold is the minimum table size (1 MiB) for a bloat warning.
// src: https://www.postgresql.org/docs/current/functions-admin.html#FUNCTIONS-ADMIN-DBOBJECT
const bloatSizeThreshold int64 = 1 * 1024 * 1024 // 1 MiB

// neverAnalyzedLiveTupThreshold is the minimum n_live_tup for flagging a never-analyzed table.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-TABLES-VIEW
const neverAnalyzedLiveTupThreshold int64 = 10_000

// schemaMessageCap is the maximum number of object names in one advice message.
const schemaMessageCap = 10

const largestRelationsN = 10

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
	SchemaName     string
	RelName        string
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

// evalUnusedIndexes returns Info advice for non-unique user indexes with zero scans and size above the threshold.
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
		ID:      "schema.index-unused",
		Level:   pgtuner.Info,
		Section: schemaSection,
		Title:   fmt.Sprintf("Unused indexes detected (%d)", len(rows)),
		Message: fmt.Sprintf("%d non-unique index(es) have idx_scan=0 and size > 1 MiB since the last statistics reset: %s%s. These are candidates for removal if the workload does not need them.",
			len(rows), strings.Join(capped, ", "), suffix),
		Detail: "Make sure these indexes are not needed (for example new indexes, or a recent statistics reset). Then drop them with DROP INDEX CONCURRENTLY. Check pg_stat_user_indexes.idx_scan for the current counts.",
		DocRef: docPGStatUserIndexes,
	}}
}

// evalInvalidIndexes returns Warn advice for indexes that are not valid or not ready.
// They come from a failed CREATE INDEX CONCURRENTLY and the planner never uses them.
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
		ID:      "schema.index-invalid",
		Level:   pgtuner.Warn,
		Section: schemaSection,
		Title:   fmt.Sprintf("Invalid index(es) detected (%d)", len(rows)),
		Message: fmt.Sprintf("%d index(es) have indisvalid=false or indisready=false in pg_index: %s%s. The build of these indexes did not succeed (typically from a failed CREATE INDEX CONCURRENTLY). The query planner does not use them. They use disk space and must be rebuilt.",
			len(rows), strings.Join(capped, ", "), suffix),
		Detail: "Drop the invalid index with DROP INDEX CONCURRENTLY. Then recreate it: CREATE INDEX CONCURRENTLY ... After creation, monitor pg_index.indisvalid to confirm success.",
		DocRef: docCreateIndexConcurrently,
	}}
}

// evalDuplicateIndexes returns Info advice for indexes on one relation with identical (indrelid, indkey, indclass, indoption, indexprs, indpred).
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
		ID:      "schema.index-duplicate",
		Level:   pgtuner.Info,
		Section: schemaSection,
		Title:   fmt.Sprintf("Duplicate indexes detected (%d group(s))", len(groups)),
		Message: fmt.Sprintf("%d group(s) of user indexes share identical definitions (same table, key columns, operator classes, options, expressions and predicate): %s%s. Redundant indexes slow writes and give no benefit to queries.",
			len(groups), strings.Join(capped, "; "), suffix),
		Detail: "Keep the index with the most meaningful name or constraint. Drop the duplicates with DROP INDEX CONCURRENTLY.",
		DocRef: docPGIndex,
	}}
}

// evalNoPrimaryKey returns Info advice for user tables above the size threshold that have no primary key.
// Such tables cannot join logical replication and cause problems for tools like pg_upgrade.
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
		ID:      "schema.table-no-primary-key",
		Level:   pgtuner.Info,
		Section: schemaSection,
		Title:   fmt.Sprintf("Tables without a primary key (%d)", len(rows)),
		Message: fmt.Sprintf("%d user table(s) larger than 1 MiB have no primary key: %s%s. Tables without a primary key cannot be part of logical replication publications. Some maintenance and backup tools require or recommend primary keys.",
			len(rows), strings.Join(capped, ", "), suffix),
		Detail: "Add a primary key with ALTER TABLE ... ADD PRIMARY KEY (...). If no natural key exists, use a generated serial, bigserial, or UUID column.",
		DocRef: docDDLConstraints,
	}}
}

// evalFKWithoutIndex returns Info advice for foreign keys whose referencing columns no user index covers.
// Without the index, deletes and updates on the referenced table scan the child table sequentially.
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
		ID:      "schema.fk-without-index",
		Level:   pgtuner.Info,
		Section: schemaSection,
		Title:   fmt.Sprintf("Foreign key(s) without a supporting index (%d)", len(rows)),
		Message: fmt.Sprintf("%d foreign key constraint(s) have no index on the referencing column(s): %s%s. Without an index, PostgreSQL must scan the child table sequentially when it deletes or updates rows in the referenced table. This causes lock escalation and slow cascades.",
			len(rows), strings.Join(capped, ", "), suffix),
		Detail: "Create an index on the FK column(s): CREATE INDEX CONCURRENTLY ON child_table(fk_col). This makes cascade deletes, referential integrity checks, and JOIN lookups faster.",
		DocRef: docDDLConstraints,
	}}
}

// evalNeverAnalyzed returns Warn advice for tables above the n_live_tup threshold that were never analyzed.
// The planner then uses stale or default statistics.
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
		ID:      "schema.table-never-analyzed",
		Level:   pgtuner.Warn,
		Section: schemaSection,
		Title:   fmt.Sprintf("Tables never analyzed (stale planner stats) (%d)", len(rows)),
		Message: fmt.Sprintf("%d table(s) with more than %d live rows have never been analyzed (last_analyze and last_autoanalyze are both NULL in pg_stat_user_tables): %s%s. The query planner uses default statistics for these tables. This can result in poor query plans.",
			len(rows), neverAnalyzedLiveTupThreshold, strings.Join(capped, ", "), suffix),
		Detail: "Run ANALYZE on the listed tables. Alternatively, make sure autovacuum and autoanalyze are enabled and not suppressed (autovacuum_enabled storage parameter). Check pg_stat_user_tables.last_autoanalyze.",
		DocRef: docSchemaStatAllTables,
	}}
}

// evalBloat returns Warn advice for tables where n_dead_tup / (n_live_tup + n_dead_tup) and the table size exceed their thresholds.
// It is an estimate: it uses stats collector counters, not on-disk page analysis.
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
		ID:      "schema.table-bloat-high",
		Level:   pgtuner.Warn,
		Section: schemaSection,
		Title:   fmt.Sprintf("Table bloat estimate: high dead-tuple ratio (%d table(s))", len(offenders)),
		Message: fmt.Sprintf("Estimate (based on pg_stat_user_tables n_dead_tup/n_live_tup, not pgstattuple): %d table(s) have dead-tuple ratio > %.0f%% and size > 1 MiB: %s%s. VACUUM may help these tables reclaim space.",
			len(offenders), bloatDeadRatioThreshold*100, strings.Join(capped, ", "), suffix),
		Detail: "Run VACUUM on the listed tables. Alternatively, check that autovacuum runs. Use VACUUM FULL only during maintenance windows, because it takes an exclusive lock. The dead-tuple ratio is an estimate. For an exact on-disk bloat measurement, use the pgstattuple extension.",
		DocRef: docSchemaStatAllTables,
	}}
}

// evalLargestRelations returns an Info summary of the largest user tables by total size, including TOAST and indexes.
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
		ID:      "schema.largest-tables",
		Level:   pgtuner.Info,
		Section: schemaSection,
		Title:   fmt.Sprintf("Largest user tables overview (top %d)", len(rows)),
		Message: fmt.Sprintf("Top %d user tables by pg_total_relation_size (includes indexes and TOAST): %s.",
			len(rows), strings.Join(parts, ", ")),
		Detail: "pg_total_relation_size includes the table heap, the TOAST table, and all indexes. For the heap only, use pg_relation_size.",
		DocRef: docObjectSizeFunctions,
	}}
}

// capList trims items to maxN and returns a suffix that counts the omitted items (empty if none).
func capList(items []string, maxN int) ([]string, string) {
	if len(items) <= maxN {
		return items, ""
	}
	return items[:maxN], fmt.Sprintf(" … and %d more", len(items)-maxN)
}

// SchemaCheck evaluates schema-level health: unused/invalid/duplicate indexes,
// tables without PKs, foreign keys without supporting indexes, never-analyzed
// tables, table bloat, and largest-relations overview.
type SchemaCheck struct{ databaseScoped }

func (SchemaCheck) Name() string { return "schema" }

// Run executes all schema sub-checks. A query failure gives one Unknown advice and the other sub-checks continue.
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
			ID:      "schema.schema-ok",
			Level:   pgtuner.OK,
			Section: schemaSection,
			Title:   "Schema looks healthy",
			Message: "pgtuner found no unused, invalid, or duplicate indexes. All user tables have primary keys. No FK constraint lacks a supporting index. All tables have been analyzed. pgtuner found no significant bloat.",
			DocRef:  docPGStatUserIndexes,
		})
	}
	return out
}

// runUnusedIndexes finds non-unique user indexes that have no scans and exceed the size threshold.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-INDEXES-VIEW
// src: https://www.postgresql.org/docs/current/catalog-pg-index.html
func runUnusedIndexes(ctx context.Context, p *Probe) []pgtuner.Advice {
	// Unique and primary-key indexes enforce constraints, so the query excludes them.
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
			ID:      "schema.index-unused-query-failed",
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
				ID:      "schema.index-unused-scan-failed",
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
			ID:      "schema.index-unused-iteration-failed",
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "Unused index iteration failed",
			Message: fmt.Sprintf("Error iterating pg_stat_user_indexes: %v", err),
			DocRef:  docPGStatUserIndexes,
		}}
	}

	return evalUnusedIndexes(result)
}

// runInvalidIndexes finds indexes with indisvalid=false or indisready=false (failed or incomplete build).
// src: https://www.postgresql.org/docs/current/sql-createindex.html
// src: https://www.postgresql.org/docs/current/catalog-pg-index.html
func runInvalidIndexes(ctx context.Context, p *Probe) []pgtuner.Advice {
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
			ID:      "schema.index-invalid-query-failed",
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
				ID:      "schema.index-invalid-scan-failed",
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
			ID:      "schema.index-invalid-iteration-failed",
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "Invalid index iteration failed",
			Message: fmt.Sprintf("Error iterating pg_index for invalid indexes: %v", err),
			DocRef:  docCreateIndexConcurrently,
		}}
	}

	return evalInvalidIndexes(result)
}

// runDuplicateIndexes finds user indexes with identical (indrelid, indkey, indclass, indoption, indexprs, indpred).
// src: https://www.postgresql.org/docs/current/catalog-pg-index.html
func runDuplicateIndexes(ctx context.Context, p *Probe) []pgtuner.Advice {
	// COALESCE makes NULL expression and predicate columns compare equal within a group.
	// t.relname is in the GROUP BY because indrelid alone does not determine it in this query.
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
			ID:      "schema.index-duplicate-query-failed",
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
				ID:      "schema.index-duplicate-scan-failed",
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
			ID:      "schema.index-duplicate-iteration-failed",
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "Duplicate index iteration failed",
			Message: fmt.Sprintf("Error iterating duplicate index groups: %v", err),
			DocRef:  docPGIndex,
		}}
	}

	return evalDuplicateIndexes(groups)
}

// runNoPrimaryKey finds user tables above the size threshold that have no primary key.
// src: https://www.postgresql.org/docs/current/ddl-constraints.html
// src: https://www.postgresql.org/docs/current/functions-admin.html#FUNCTIONS-ADMIN-DBOBJECT
func runNoPrimaryKey(ctx context.Context, p *Probe) []pgtuner.Advice {
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
			ID:      "schema.table-no-primary-key-query-failed",
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
				ID:      "schema.table-no-primary-key-scan-failed",
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
			ID:      "schema.table-no-primary-key-iteration-failed",
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "No-primary-key iteration failed",
			Message: fmt.Sprintf("Error iterating no-PK table rows: %v", err),
			DocRef:  docDDLConstraints,
		}}
	}

	return evalNoPrimaryKey(result)
}

// runFKWithoutIndex finds foreign keys whose referencing columns no valid index on the child table covers.
// It uses @> containment of conkey in indkey, which ignores column order. Prefix matching is more precise for btree,
// but containment avoids access-method branching.
// src: https://www.postgresql.org/docs/current/ddl-constraints.html
// src: https://www.postgresql.org/docs/current/catalog-pg-index.html
// src: https://www.postgresql.org/docs/current/catalog-pg-constraint.html
func runFKWithoutIndex(ctx context.Context, p *Probe) []pgtuner.Advice {
	// indkey is int2vector, so the query casts it to int2[] for the @> operator.
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
			ID:      "schema.fk-without-index-query-failed",
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
				ID:      "schema.fk-without-index-scan-failed",
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
			ID:      "schema.fk-without-index-iteration-failed",
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "FK-without-index iteration failed",
			Message: fmt.Sprintf("Error iterating FK-without-index rows: %v", err),
			DocRef:  docDDLConstraints,
		}}
	}

	return evalFKWithoutIndex(result)
}

// runNeverAnalyzed finds tables above the n_live_tup threshold where last_analyze and last_autoanalyze are both NULL.
// It also records tables where last_vacuum and last_autovacuum are both NULL.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-TABLES-VIEW
func runNeverAnalyzed(ctx context.Context, p *Probe) []pgtuner.Advice {
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
			ID:      "schema.table-never-analyzed-query-failed",
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
				ID:      "schema.table-never-analyzed-scan-failed",
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
			ID:      "schema.table-never-analyzed-iteration-failed",
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "Never-analyzed table iteration failed",
			Message: fmt.Sprintf("Error iterating never-analyzed table rows: %v", err),
			DocRef:  docSchemaStatAllTables,
		}}
	}

	return evalNeverAnalyzed(result)
}

// runBloat reads dead and live tuple counters. Results are ordered by dead-tuple count descending.
// The ratio is an estimate from stats collector counters, not an on-disk measurement.
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
			ID:      "schema.table-bloat-query-failed",
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
				ID:      "schema.table-bloat-scan-failed",
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
			ID:      "schema.table-bloat-iteration-failed",
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "Table bloat iteration failed",
			Message: fmt.Sprintf("Error iterating bloat table rows: %v", err),
			DocRef:  docSchemaStatAllTables,
		}}
	}

	return evalBloat(result)
}

// runLargestRelations finds the largest user tables by pg_total_relation_size (heap, TOAST, indexes).
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
			ID:      "schema.largest-tables-query-failed",
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
				ID:      "schema.largest-tables-scan-failed",
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
			ID:      "schema.largest-tables-iteration-failed",
			Level:   pgtuner.Unknown,
			Section: schemaSection,
			Title:   "Largest relations iteration failed",
			Message: fmt.Sprintf("Error iterating largest relation rows: %v", err),
			DocRef:  docObjectSizeFunctions,
		}}
	}

	return evalLargestRelations(result)
}
