package checks

import (
	"testing"

	"github.com/soylic/pgtuner/pkg/pgtuner"
)

func TestCapList_BelowCap(t *testing.T) {
	items := []string{"a", "b", "c"}
	got, suffix := capList(items, 5)
	if len(got) != 3 {
		t.Fatalf("expected 3 items, got %d", len(got))
	}
	if suffix != "" {
		t.Fatalf("expected empty suffix, got %q", suffix)
	}
}

func TestCapList_AtCap(t *testing.T) {
	items := []string{"a", "b", "c"}
	got, suffix := capList(items, 3)
	if len(got) != 3 {
		t.Fatalf("expected 3 items at cap, got %d", len(got))
	}
	if suffix != "" {
		t.Fatalf("expected empty suffix at cap, got %q", suffix)
	}
}

func TestCapList_AboveCap(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}
	got, suffix := capList(items, 3)
	if len(got) != 3 {
		t.Fatalf("expected 3 items after cap, got %d", len(got))
	}
	if suffix == "" {
		t.Fatal("expected non-empty suffix when capped")
	}
}

func TestCapList_Empty(t *testing.T) {
	got, suffix := capList(nil, 5)
	if len(got) != 0 {
		t.Fatalf("expected 0 items, got %d", len(got))
	}
	if suffix != "" {
		t.Fatalf("expected empty suffix for nil input, got %q", suffix)
	}
}

func TestEvalUnusedIndexes_None(t *testing.T) {
	out := evalUnusedIndexes(nil)
	if len(out) != 0 {
		t.Fatalf("expected no advice for empty rows, got %v", levelsOf(out))
	}
}

func TestEvalUnusedIndexes_OneRow_Info(t *testing.T) {
	rows := []UnusedIndexRow{
		{SchemaName: "public", RelName: "orders", IndexName: "idx_orders_old", SizeBytes: 2 * 1024 * 1024},
	}
	out := evalUnusedIndexes(rows)
	if len(out) == 0 || out[0].Level != pgtuner.Info {
		t.Fatalf("expected Info for one unused index, got %v", levelsOf(out))
	}
	if out[0].Title == "" || out[0].Message == "" {
		t.Fatal("expected non-empty title and message")
	}
}

func TestEvalUnusedIndexes_ManyRows_Capped(t *testing.T) {
	var rows []UnusedIndexRow
	for i := 0; i < schemaMessageCap+5; i++ {
		rows = append(rows, UnusedIndexRow{
			SchemaName: "public",
			RelName:    "tbl",
			IndexName:  "idx_extra",
			SizeBytes:  1024 * 1024 * 2,
		})
	}
	out := evalUnusedIndexes(rows)
	if len(out) == 0 || out[0].Level != pgtuner.Info {
		t.Fatalf("expected Info for many unused indexes, got %v", levelsOf(out))
	}
	if !containsStr(out[0].Message, "more") {
		t.Errorf("expected message to mention 'more' when capped, got: %s", out[0].Message)
	}
}

func TestEvalInvalidIndexes_None(t *testing.T) {
	out := evalInvalidIndexes(nil)
	if len(out) != 0 {
		t.Fatalf("expected no advice for empty invalid index rows, got %v", levelsOf(out))
	}
}

func TestEvalInvalidIndexes_OneRow_Warn(t *testing.T) {
	rows := []InvalidIndexRow{
		{SchemaName: "public", RelName: "orders", IndexName: "idx_orders_broken"},
	}
	out := evalInvalidIndexes(rows)
	if len(out) == 0 || out[0].Level != pgtuner.Warn {
		t.Fatalf("expected Warn for invalid index, got %v", levelsOf(out))
	}
	if out[0].Title == "" || out[0].Message == "" {
		t.Fatal("expected non-empty title and message")
	}
}

func TestEvalInvalidIndexes_MultipleRows_Warn(t *testing.T) {
	rows := []InvalidIndexRow{
		{SchemaName: "public", RelName: "t1", IndexName: "idx1"},
		{SchemaName: "public", RelName: "t2", IndexName: "idx2"},
	}
	out := evalInvalidIndexes(rows)
	if len(out) == 0 || out[0].Level != pgtuner.Warn {
		t.Fatalf("expected Warn for multiple invalid indexes, got %v", levelsOf(out))
	}
}

func TestEvalDuplicateIndexes_None(t *testing.T) {
	out := evalDuplicateIndexes(nil)
	if len(out) != 0 {
		t.Fatalf("expected no advice for empty duplicate groups, got %v", levelsOf(out))
	}
}

func TestEvalDuplicateIndexes_OneGroup_Info(t *testing.T) {
	groups := []DuplicateIndexGroup{
		{RelName: "orders", IndexNames: []string{"idx_orders_a", "idx_orders_b"}},
	}
	out := evalDuplicateIndexes(groups)
	if len(out) == 0 || out[0].Level != pgtuner.Info {
		t.Fatalf("expected Info for duplicate indexes, got %v", levelsOf(out))
	}
	if out[0].Title == "" || out[0].Message == "" {
		t.Fatal("expected non-empty title and message")
	}
}

func TestEvalDuplicateIndexes_MultipleGroups_Info(t *testing.T) {
	groups := []DuplicateIndexGroup{
		{RelName: "t1", IndexNames: []string{"idx_t1_a", "idx_t1_b"}},
		{RelName: "t2", IndexNames: []string{"idx_t2_x", "idx_t2_y", "idx_t2_z"}},
	}
	out := evalDuplicateIndexes(groups)
	if len(out) == 0 || out[0].Level != pgtuner.Info {
		t.Fatalf("expected Info for multiple duplicate groups, got %v", levelsOf(out))
	}
}

func TestEvalNoPrimaryKey_None(t *testing.T) {
	out := evalNoPrimaryKey(nil)
	if len(out) != 0 {
		t.Fatalf("expected no advice for empty no-PK rows, got %v", levelsOf(out))
	}
}

func TestEvalNoPrimaryKey_OneRow_Info(t *testing.T) {
	rows := []NoPKRow{
		{SchemaName: "public", RelName: "log_table", SizeBytes: 5 * 1024 * 1024},
	}
	out := evalNoPrimaryKey(rows)
	if len(out) == 0 || out[0].Level != pgtuner.Info {
		t.Fatalf("expected Info for no-PK table, got %v", levelsOf(out))
	}
	if out[0].Title == "" || out[0].Message == "" {
		t.Fatal("expected non-empty title and message")
	}
}

func TestEvalNoPrimaryKey_MultipleRows_Info(t *testing.T) {
	rows := []NoPKRow{
		{SchemaName: "public", RelName: "a", SizeBytes: 2 * 1024 * 1024},
		{SchemaName: "public", RelName: "b", SizeBytes: 3 * 1024 * 1024},
	}
	out := evalNoPrimaryKey(rows)
	if len(out) == 0 || out[0].Level != pgtuner.Info {
		t.Fatalf("expected Info for multiple no-PK tables, got %v", levelsOf(out))
	}
}

func TestEvalFKWithoutIndex_None(t *testing.T) {
	out := evalFKWithoutIndex(nil)
	if len(out) != 0 {
		t.Fatalf("expected no advice for empty FK rows, got %v", levelsOf(out))
	}
}

func TestEvalFKWithoutIndex_OneRow_Info(t *testing.T) {
	rows := []FKNoIndexRow{
		{SchemaName: "public", RelName: "order_items", ConstraintName: "order_items_order_id_fkey"},
	}
	out := evalFKWithoutIndex(rows)
	if len(out) == 0 || out[0].Level != pgtuner.Info {
		t.Fatalf("expected Info for FK without index, got %v", levelsOf(out))
	}
	if out[0].Title == "" || out[0].Message == "" {
		t.Fatal("expected non-empty title and message")
	}
}

func TestEvalFKWithoutIndex_MultipleRows_Info(t *testing.T) {
	rows := []FKNoIndexRow{
		{SchemaName: "public", RelName: "a", ConstraintName: "a_b_fkey"},
		{SchemaName: "public", RelName: "c", ConstraintName: "c_d_fkey"},
	}
	out := evalFKWithoutIndex(rows)
	if len(out) == 0 || out[0].Level != pgtuner.Info {
		t.Fatalf("expected Info for multiple FKs without index, got %v", levelsOf(out))
	}
}

func TestEvalNeverAnalyzed_None(t *testing.T) {
	out := evalNeverAnalyzed(nil)
	if len(out) != 0 {
		t.Fatalf("expected no advice for empty never-analyzed rows, got %v", levelsOf(out))
	}
}

func TestEvalNeverAnalyzed_OneRow_Warn(t *testing.T) {
	rows := []NeverAnalyzedRow{
		{SchemaName: "public", RelName: "events", LiveTuples: 50_000, NeverVacuum: false},
	}
	out := evalNeverAnalyzed(rows)
	if len(out) == 0 || out[0].Level != pgtuner.Warn {
		t.Fatalf("expected Warn for never-analyzed table, got %v", levelsOf(out))
	}
	if out[0].Title == "" || out[0].Message == "" {
		t.Fatal("expected non-empty title and message")
	}
}

func TestEvalNeverAnalyzed_NeverVacuumedFlag(t *testing.T) {
	rows := []NeverAnalyzedRow{
		{SchemaName: "public", RelName: "events", LiveTuples: 50_000, NeverVacuum: true},
	}
	out := evalNeverAnalyzed(rows)
	if len(out) == 0 {
		t.Fatal("expected at least one advice item")
	}
	if !containsStr(out[0].Message, "never vacuumed") {
		t.Errorf("expected 'never vacuumed' in message when NeverVacuum=true, got: %s", out[0].Message)
	}
}

func TestEvalNeverAnalyzed_MultipleRows_Warn(t *testing.T) {
	rows := []NeverAnalyzedRow{
		{SchemaName: "public", RelName: "a", LiveTuples: 20_000},
		{SchemaName: "public", RelName: "b", LiveTuples: 30_000},
	}
	out := evalNeverAnalyzed(rows)
	if len(out) == 0 || out[0].Level != pgtuner.Warn {
		t.Fatalf("expected Warn for multiple never-analyzed tables, got %v", levelsOf(out))
	}
}

func TestEvalBloat_None(t *testing.T) {
	out := evalBloat(nil)
	if len(out) != 0 {
		t.Fatalf("expected no advice for empty bloat rows, got %v", levelsOf(out))
	}
}

func TestEvalBloat_BelowRatioThreshold_NoAdvice(t *testing.T) {
	// dead_ratio = 0.10 < 0.20 threshold → no advice.
	// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-TABLES-VIEW
	rows := []BloatRow{
		{SchemaName: "public", RelName: "t", DeadTuples: 1000, LiveTuples: 9000, SizeBytes: 10 * 1024 * 1024},
	}
	out := evalBloat(rows)
	if len(out) != 0 {
		t.Fatalf("expected no advice for dead_ratio=10%%, got %v", levelsOf(out))
	}
}

func TestEvalBloat_AtRatioThreshold_NoAdvice(t *testing.T) {
	// dead_ratio = 0.20 exactly → no advice (condition is > threshold, exclusive).
	rows := []BloatRow{
		{SchemaName: "public", RelName: "t", DeadTuples: 2000, LiveTuples: 8000, SizeBytes: 10 * 1024 * 1024},
	}
	out := evalBloat(rows)
	if len(out) != 0 {
		t.Fatalf("expected no advice at exact bloat threshold=20%%, got %v", levelsOf(out))
	}
}

func TestEvalBloat_AboveRatioThreshold_LargeTable_Warn(t *testing.T) {
	rows := []BloatRow{
		{SchemaName: "public", RelName: "orders", DeadTuples: 3000, LiveTuples: 7000, SizeBytes: 5 * 1024 * 1024},
	}
	out := evalBloat(rows)
	if len(out) == 0 || out[0].Level != pgtuner.Warn {
		t.Fatalf("expected Warn for dead_ratio=30%% on large table, got %v", levelsOf(out))
	}
	if out[0].Title == "" || out[0].Message == "" {
		t.Fatal("expected non-empty title and message")
	}
	// The bloat figure is an estimate, so the message must say so.
	if !containsStr(out[0].Message, "stimate") {
		t.Errorf("expected bloat message to mention 'estimate', got: %s", out[0].Message)
	}
}

func TestEvalBloat_AboveRatioThreshold_SmallTable_NoAdvice(t *testing.T) {
	rows := []BloatRow{
		{SchemaName: "public", RelName: "tiny", DeadTuples: 300, LiveTuples: 700, SizeBytes: 512 * 1024},
	}
	out := evalBloat(rows)
	if len(out) != 0 {
		t.Fatalf("expected no advice for bloated but small table, got %v", levelsOf(out))
	}
}

func TestEvalBloat_ZeroTotal_NoAdvice(t *testing.T) {
	rows := []BloatRow{
		{SchemaName: "public", RelName: "empty", DeadTuples: 0, LiveTuples: 0, SizeBytes: 5 * 1024 * 1024},
	}
	out := evalBloat(rows)
	if len(out) != 0 {
		t.Fatalf("expected no advice for zero-total table, got %v", levelsOf(out))
	}
}

func TestEvalBloat_MultipleOffenders_Warn(t *testing.T) {
	rows := []BloatRow{
		{SchemaName: "public", RelName: "a", DeadTuples: 3000, LiveTuples: 7000, SizeBytes: 5 * 1024 * 1024},
		{SchemaName: "public", RelName: "b", DeadTuples: 5000, LiveTuples: 3000, SizeBytes: 8 * 1024 * 1024},
	}
	out := evalBloat(rows)
	if len(out) == 0 || out[0].Level != pgtuner.Warn {
		t.Fatalf("expected Warn for multiple bloated tables, got %v", levelsOf(out))
	}
}

func TestEvalLargestRelations_None(t *testing.T) {
	out := evalLargestRelations(nil)
	if len(out) != 0 {
		t.Fatalf("expected no advice for empty largest-relations rows, got %v", levelsOf(out))
	}
}

func TestEvalLargestRelations_SomeRows_Info(t *testing.T) {
	// src: https://www.postgresql.org/docs/current/functions-admin.html#FUNCTIONS-ADMIN-DBOBJECT
	rows := []LargestRelRow{
		{SchemaName: "public", RelName: "orders", TotalBytes: 100 * 1024 * 1024},
		{SchemaName: "public", RelName: "items", TotalBytes: 50 * 1024 * 1024},
	}
	out := evalLargestRelations(rows)
	if len(out) == 0 || out[0].Level != pgtuner.Info {
		t.Fatalf("expected Info for largest relations, got %v", levelsOf(out))
	}
	if out[0].Title == "" || out[0].Message == "" {
		t.Fatal("expected non-empty title and message")
	}
}

func TestEvalLargestRelations_SizeInMessage(t *testing.T) {
	rows := []LargestRelRow{
		{SchemaName: "public", RelName: "big_table", TotalBytes: 1024 * 1024 * 1024}, // 1 GiB
	}
	out := evalLargestRelations(rows)
	if len(out) == 0 {
		t.Fatal("expected at least one advice item")
	}
	if !containsStr(out[0].Message, "big_table") {
		t.Errorf("expected message to contain table name 'big_table', got: %s", out[0].Message)
	}
}
