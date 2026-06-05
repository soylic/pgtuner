package checks

import (
	"fmt"
	"strings"
	"testing"

	"github.com/OpenRemedy/pgtuner/internal/pg"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// ----- queryStatsColumns -------------------------------------------------------

// TestQueryStatsColumns_PG12 verifies the PG12 branch returns legacy column names.
// PG ≤ 12 used total_time and mean_time (confirmed live against postgres:12-alpine).
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
func TestQueryStatsColumns_PG12(t *testing.T) {
	v := pg.ServerVersion{Major: 12, Minor: 0}
	totalCol, meanCol := queryStatsColumns(v)
	if totalCol != "total_time" {
		t.Errorf("PG12: expected totalCol='total_time', got %q", totalCol)
	}
	if meanCol != "mean_time" {
		t.Errorf("PG12: expected meanCol='mean_time', got %q", meanCol)
	}
}

// TestQueryStatsColumns_PG12Late verifies PG12.99 still returns legacy names.
func TestQueryStatsColumns_PG12Late(t *testing.T) {
	v := pg.ServerVersion{Major: 12, Minor: 9999}
	totalCol, meanCol := queryStatsColumns(v)
	if totalCol != "total_time" {
		t.Errorf("PG12.9999: expected totalCol='total_time', got %q", totalCol)
	}
	if meanCol != "mean_time" {
		t.Errorf("PG12.9999: expected meanCol='mean_time', got %q", meanCol)
	}
}

// TestQueryStatsColumns_PG13 verifies the PG13 branch returns renamed column names.
// The rename from total_time→total_exec_time happened in PG13.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
func TestQueryStatsColumns_PG13(t *testing.T) {
	v := pg.ServerVersion{Major: 13, Minor: 0}
	totalCol, meanCol := queryStatsColumns(v)
	if totalCol != "total_exec_time" {
		t.Errorf("PG13: expected totalCol='total_exec_time', got %q", totalCol)
	}
	if meanCol != "mean_exec_time" {
		t.Errorf("PG13: expected meanCol='mean_exec_time', got %q", meanCol)
	}
}

// TestQueryStatsColumns_PG14 verifies PG14 uses the new column names.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
func TestQueryStatsColumns_PG14(t *testing.T) {
	v := pg.ServerVersion{Major: 14, Minor: 0}
	totalCol, meanCol := queryStatsColumns(v)
	if totalCol != "total_exec_time" {
		t.Errorf("PG14: expected totalCol='total_exec_time', got %q", totalCol)
	}
	if meanCol != "mean_exec_time" {
		t.Errorf("PG14: expected meanCol='mean_exec_time', got %q", meanCol)
	}
}

// TestQueryStatsColumns_PG16 verifies PG16 (current LTS) uses the new column names.
func TestQueryStatsColumns_PG16(t *testing.T) {
	v := pg.ServerVersion{Major: 16, Minor: 0}
	totalCol, meanCol := queryStatsColumns(v)
	if totalCol != "total_exec_time" {
		t.Errorf("PG16: expected totalCol='total_exec_time', got %q", totalCol)
	}
	if meanCol != "mean_exec_time" {
		t.Errorf("PG16: expected meanCol='mean_exec_time', got %q", meanCol)
	}
}

// TestQueryStatsColumns_PG18 verifies PG18 (future) also uses the new column names.
func TestQueryStatsColumns_PG18(t *testing.T) {
	v := pg.ServerVersion{Major: 18, Minor: 0}
	totalCol, meanCol := queryStatsColumns(v)
	if totalCol != "total_exec_time" {
		t.Errorf("PG18: expected totalCol='total_exec_time', got %q", totalCol)
	}
	if meanCol != "mean_exec_time" {
		t.Errorf("PG18: expected meanCol='mean_exec_time', got %q", meanCol)
	}
}

// ----- truncateQuery -----------------------------------------------------------

// TestTruncateQuery_Short verifies that a short query is returned unchanged.
func TestTruncateQuery_Short(t *testing.T) {
	q := "SELECT 1"
	got := truncateQuery(q)
	if got != q {
		t.Errorf("expected %q, got %q", q, got)
	}
}

// TestTruncateQuery_Whitespace verifies that internal whitespace is collapsed.
func TestTruncateQuery_Whitespace(t *testing.T) {
	q := "SELECT  \t\n  1  "
	got := truncateQuery(q)
	if got != "SELECT 1" {
		t.Errorf("expected 'SELECT 1', got %q", got)
	}
}

// TestTruncateQuery_Long verifies that a query longer than queryStatsQueryTruncLen
// runes is truncated and suffixed with "…".
func TestTruncateQuery_Long(t *testing.T) {
	// Build a query longer than queryStatsQueryTruncLen (100) runes.
	q := "SELECT " + strings.Repeat("x,", 60) + "y FROM t"
	got := truncateQuery(q)
	runes := []rune(got)
	if len(runes) > queryStatsQueryTruncLen+len([]rune("…")) {
		t.Errorf("truncated query too long: %d runes (truncLen=%d), got: %s",
			len(runes), queryStatsQueryTruncLen, got)
	}
	if !containsStr(got, "…") {
		t.Errorf("expected truncation ellipsis in long query output, got: %q", got)
	}
}

// TestTruncateQuery_ExactBoundary verifies a query of exactly queryStatsQueryTruncLen
// runes is returned unchanged (no ellipsis).
func TestTruncateQuery_ExactBoundary(t *testing.T) {
	// Build a query of exactly 100 runes (queryStatsQueryTruncLen).
	q := strings.Repeat("x", queryStatsQueryTruncLen)
	got := truncateQuery(q)
	if got != q {
		t.Errorf("expected unchanged query at exact truncLen boundary, got %q", got)
	}
	if containsStr(got, "…") {
		t.Errorf("unexpected ellipsis at boundary, got: %q", got)
	}
}

// ----- evalTopQueryByTime ------------------------------------------------------

// TestEvalTopQueryByTime_Empty verifies no advice is emitted for an empty slice.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
func TestEvalTopQueryByTime_Empty(t *testing.T) {
	got := evalTopQueryByTime(nil)
	if len(got) != 0 {
		t.Fatalf("expected no advice for empty rows, got %d", len(got))
	}
}

// TestEvalTopQueryByTime_SingleRow verifies a single row produces an Info with
// the query's metrics in the message.
func TestEvalTopQueryByTime_SingleRow(t *testing.T) {
	rows := []queryStatsRow{
		{Query: "SELECT count(*) FROM orders", Calls: 1000, TotalExecMs: 5000.0, MeanExecMs: 5.0, Rows: 1000},
	}
	got := evalTopQueryByTime(rows)
	if len(got) != 1 {
		t.Fatalf("expected 1 advice, got %d", len(got))
	}
	a := got[0]
	if a.Level != pgtuner.Info {
		t.Errorf("expected Info, got %s", a.Level)
	}
	if !containsStr(a.Message, "SELECT count") {
		t.Errorf("expected query text in message, got: %s", a.Message)
	}
	if !containsStr(a.Message, "5000") {
		t.Errorf("expected total time in message, got: %s", a.Message)
	}
	if !containsStr(a.Message, "1000") {
		t.Errorf("expected calls in message, got: %s", a.Message)
	}
}

// TestEvalTopQueryByTime_CapAtTopN verifies that when more than queryStatsTopN
// rows are provided only the first queryStatsTopN are shown in the message.
func TestEvalTopQueryByTime_CapAtTopN(t *testing.T) {
	rows := make([]queryStatsRow, queryStatsTopN+3)
	for i := range rows {
		rows[i] = queryStatsRow{
			Query:       fmt.Sprintf("SELECT %d FROM t", i),
			Calls:       int64(i + 1),
			TotalExecMs: float64((queryStatsTopN + 3 - i) * 100),
			MeanExecMs:  1.0,
		}
	}
	got := evalTopQueryByTime(rows)
	if len(got) != 1 {
		t.Fatalf("expected 1 advice, got %d", len(got))
	}
	// The title must reflect the number of rows shown (queryStatsTopN), not the total.
	if !containsStr(got[0].Title, fmt.Sprintf("%d", queryStatsTopN)) {
		t.Errorf("expected topN count %d in title, got: %s", queryStatsTopN, got[0].Title)
	}
}

// ----- evalLowCacheHitQueries --------------------------------------------------

// TestEvalLowCacheHitQueries_Empty verifies no advice for an empty set.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
func TestEvalLowCacheHitQueries_Empty(t *testing.T) {
	got := evalLowCacheHitQueries(nil)
	if len(got) != 0 {
		t.Fatalf("expected no advice for empty rows, got %d", len(got))
	}
}

// TestEvalLowCacheHitQueries_HighHitRatio verifies no advice when hit ratio
// is at the threshold (0.90 = not below, so no advice).
func TestEvalLowCacheHitQueries_HighHitRatio(t *testing.T) {
	// 9000 hit + 1000 read = 10000 total; ratio = 0.90 (at boundary, not below).
	rows := []queryStatsRow{
		{Query: "SELECT 1", Calls: 1, SharedBlksHit: 9000, SharedBlksRead: 1000},
	}
	got := evalLowCacheHitQueries(rows)
	if len(got) != 0 {
		t.Fatalf("expected no advice at exactly 0.90 hit ratio, got %d: %v", len(got), got)
	}
}

// TestEvalLowCacheHitQueries_BelowThreshold verifies Info when hit ratio < 0.90
// and total blocks >= cacheHitMinTotalBlocks.
func TestEvalLowCacheHitQueries_BelowThreshold(t *testing.T) {
	// 8000 hit + 2001 read = 10001 total; ratio ≈ 0.7999 (below 0.90).
	rows := []queryStatsRow{
		{Query: "SELECT big FROM tbl", Calls: 5, SharedBlksHit: 8000, SharedBlksRead: 2001},
	}
	got := evalLowCacheHitQueries(rows)
	if len(got) != 1 {
		t.Fatalf("expected 1 advice for low hit ratio, got %d", len(got))
	}
	a := got[0]
	if a.Level != pgtuner.Info {
		t.Errorf("expected Info, got %s", a.Level)
	}
	if !containsStr(a.Message, "SELECT big") {
		t.Errorf("expected query text in message, got: %s", a.Message)
	}
}

// TestEvalLowCacheHitQueries_BelowMinBlocks verifies no advice when total blocks
// is below cacheHitMinTotalBlocks, even if hit ratio is low.  Small queries have
// noisy ratios.
func TestEvalLowCacheHitQueries_BelowMinBlocks(t *testing.T) {
	// 5 hit + 5 read = 10 total (way below cacheHitMinTotalBlocks=1000).
	rows := []queryStatsRow{
		{Query: "SELECT 1", Calls: 1, SharedBlksHit: 5, SharedBlksRead: 5},
	}
	got := evalLowCacheHitQueries(rows)
	if len(got) != 0 {
		t.Fatalf("expected no advice below minBlocks threshold, got %d", len(got))
	}
}

// TestEvalLowCacheHitQueries_AllReads verifies Info when all accesses are reads
// (hit=0) and total blocks is significant.
func TestEvalLowCacheHitQueries_AllReads(t *testing.T) {
	rows := []queryStatsRow{
		{Query: "SELECT * FROM large_table", Calls: 10, SharedBlksHit: 0, SharedBlksRead: 5000},
	}
	got := evalLowCacheHitQueries(rows)
	if len(got) != 1 || got[0].Level != pgtuner.Info {
		t.Fatalf("expected 1 Info for all-reads query, got %d: %v", len(got), got)
	}
}

// TestEvalLowCacheHitQueries_AllHits verifies no advice when all accesses are hits.
func TestEvalLowCacheHitQueries_AllHits(t *testing.T) {
	rows := []queryStatsRow{
		{Query: "SELECT 1", Calls: 100, SharedBlksHit: 50000, SharedBlksRead: 0},
	}
	got := evalLowCacheHitQueries(rows)
	if len(got) != 0 {
		t.Fatalf("expected no advice for all-hits query, got %d", len(got))
	}
}

// ----- evalTempHeavyQueries ----------------------------------------------------

// TestEvalTempHeavyQueries_Empty verifies no advice for an empty set.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
func TestEvalTempHeavyQueries_Empty(t *testing.T) {
	got := evalTempHeavyQueries(nil)
	if len(got) != 0 {
		t.Fatalf("expected no advice for empty rows, got %d", len(got))
	}
}

// TestEvalTempHeavyQueries_ZeroTemp verifies no advice when temp_blks_written is 0.
func TestEvalTempHeavyQueries_ZeroTemp(t *testing.T) {
	rows := []queryStatsRow{
		{Query: "SELECT order_id FROM orders ORDER BY amount", Calls: 1000, TempBlksWritten: 0},
	}
	got := evalTempHeavyQueries(rows)
	if len(got) != 0 {
		t.Fatalf("expected no advice for zero temp writes, got %d", len(got))
	}
}

// TestEvalTempHeavyQueries_BelowThreshold verifies no advice when temp writes
// are below the meaningful threshold (tempBlksWrittenThreshold).
func TestEvalTempHeavyQueries_BelowThreshold(t *testing.T) {
	// tempBlksWrittenThreshold = 128; below that is noise.
	rows := []queryStatsRow{
		{Query: "SELECT 1 ORDER BY random()", Calls: 1, TempBlksWritten: tempBlksWrittenThreshold - 1},
	}
	got := evalTempHeavyQueries(rows)
	if len(got) != 0 {
		t.Fatalf("expected no advice below threshold, got %d", len(got))
	}
}

// TestEvalTempHeavyQueries_AtThreshold verifies Info when temp writes are exactly
// at the threshold (tempBlksWrittenThreshold is an inclusive lower bound: >=).
func TestEvalTempHeavyQueries_AtThreshold(t *testing.T) {
	rows := []queryStatsRow{
		{Query: "SELECT * FROM big ORDER BY col", Calls: 5, TempBlksWritten: tempBlksWrittenThreshold},
	}
	got := evalTempHeavyQueries(rows)
	if len(got) != 1 {
		t.Fatalf("expected 1 advice at threshold, got %d", len(got))
	}
	if got[0].Level != pgtuner.Info {
		t.Errorf("expected Info, got %s", got[0].Level)
	}
}

// TestEvalTempHeavyQueries_AboveThreshold verifies Info for a significant spill.
func TestEvalTempHeavyQueries_AboveThreshold(t *testing.T) {
	rows := []queryStatsRow{
		{Query: "SELECT * FROM events ORDER BY created_at", Calls: 100, TempBlksWritten: 10000},
	}
	got := evalTempHeavyQueries(rows)
	if len(got) != 1 || got[0].Level != pgtuner.Info {
		t.Fatalf("expected 1 Info for large temp spill, got %d: %v", len(got), got)
	}
	if !containsStr(got[0].Message, "events") {
		t.Errorf("expected query text 'events' in message, got: %s", got[0].Message)
	}
	if !containsStr(got[0].Detail, "work_mem") {
		t.Errorf("expected work_mem advice in Detail, got: %s", got[0].Detail)
	}
}

// TestEvalTempHeavyQueries_MultipleRows verifies that multiple temp-heavy queries
// are all detected and capped to queryStatsTopN in the message.
func TestEvalTempHeavyQueries_MultipleRows(t *testing.T) {
	rows := make([]queryStatsRow, queryStatsTopN+2)
	for i := range rows {
		rows[i] = queryStatsRow{
			Query:           fmt.Sprintf("SELECT %d FROM big ORDER BY col", i),
			Calls:           int64(i + 1),
			TempBlksWritten: int64(tempBlksWrittenThreshold + i*100),
		}
	}
	got := evalTempHeavyQueries(rows)
	if len(got) != 1 {
		t.Fatalf("expected 1 combined advice, got %d", len(got))
	}
	// Title should mention the total count of offenders (all rows qualify).
	if !containsStr(got[0].Title, fmt.Sprintf("%d", len(rows))) {
		t.Errorf("expected offender count %d in title, got: %s", len(rows), got[0].Title)
	}
}

// ----- misc ------------------------------------------------------------------

// TestQueryStatsCheck_Name verifies the check name.
func TestQueryStatsCheck_Name(t *testing.T) {
	var c QueryStatsCheck
	if c.Name() != "querystats" {
		t.Errorf("expected name 'querystats', got %q", c.Name())
	}
}
