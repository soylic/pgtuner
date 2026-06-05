package checks

import (
	"strings"
	"testing"

	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// ----- evalDeadTuples --------------------------------------------------------

func TestEvalDeadTuples_NoRows(t *testing.T) {
	got := evalDeadTuples(nil, 50, 0.2)
	if len(got) != 0 {
		t.Fatalf("expected no advice for empty rows, got %v", levelsOf(got))
	}
}

func TestEvalDeadTuples_BelowThreshold(t *testing.T) {
	// 40 dead tuples on a table with 1000 live: trigger = 50 + 0.2*1000 = 250; 40 < 250 → no advice
	rows := []DeadTupleRow{
		{SchemaName: "public", RelName: "users", DeadTuples: 40, LiveTuples: 1000},
	}
	got := evalDeadTuples(rows, 50, 0.2)
	if len(got) != 0 {
		t.Fatalf("expected no advice below threshold, got %v", levelsOf(got))
	}
}

func TestEvalDeadTuples_ExactlyAtThreshold(t *testing.T) {
	// Exactly at threshold (= trigger) → NOT above, so no advice.
	// trigger = 50 + 0.2 * 1000 = 250; n_dead_tup = 250 → not > 250
	rows := []DeadTupleRow{
		{SchemaName: "public", RelName: "orders", DeadTuples: 250, LiveTuples: 1000},
	}
	got := evalDeadTuples(rows, 50, 0.2)
	if len(got) != 0 {
		t.Fatalf("expected no advice at exactly the threshold, got %v", levelsOf(got))
	}
}

func TestEvalDeadTuples_AboveThreshold(t *testing.T) {
	// 300 dead on 1000 live: trigger = 250; 300 > 250 → Warn
	rows := []DeadTupleRow{
		{SchemaName: "public", RelName: "orders", DeadTuples: 300, LiveTuples: 1000},
	}
	got := evalDeadTuples(rows, 50, 0.2)
	if len(got) == 0 || got[0].Level != pgtuner.Warn {
		t.Fatalf("expected Warn for dead tuples above threshold, got %v", levelsOf(got))
	}
}

func TestEvalDeadTuples_MultipleOffenders(t *testing.T) {
	// Three tables all above threshold.
	rows := []DeadTupleRow{
		{SchemaName: "public", RelName: "a", DeadTuples: 1000, LiveTuples: 100},
		{SchemaName: "public", RelName: "b", DeadTuples: 2000, LiveTuples: 200},
		{SchemaName: "public", RelName: "c", DeadTuples: 3000, LiveTuples: 300},
	}
	// trigger for each = 50 + 0.2 * 100 = 70; all dead_tup values >> 70
	got := evalDeadTuples(rows, 50, 0.2)
	if len(got) == 0 || got[0].Level != pgtuner.Warn {
		t.Fatalf("expected Warn for multiple offenders, got %v", levelsOf(got))
	}
	// Message should mention the table names.
	if got[0].Message == "" {
		t.Fatal("expected non-empty message")
	}
}

func TestEvalDeadTuples_CapMessage(t *testing.T) {
	// More than deadTupleMessageCap offenders: message should mention "more".
	var rows []DeadTupleRow
	for i := 0; i < deadTupleMessageCap+2; i++ {
		rows = append(rows, DeadTupleRow{
			SchemaName: "public",
			RelName:    "t",
			DeadTuples: 10000,
			LiveTuples: 0,
		})
	}
	got := evalDeadTuples(rows, 50, 0.2)
	if len(got) == 0 {
		t.Fatal("expected at least one advice")
	}
	msg := got[0].Message
	if len(msg) == 0 {
		t.Fatal("expected non-empty message")
	}
	if !strings.Contains(msg, "more") {
		t.Fatalf("expected '... and N more' in capped message, got: %s", msg)
	}
}

// ----- evalWraparound --------------------------------------------------------

func TestEvalWraparound_Safe(t *testing.T) {
	// 10% of maxAge → nil (safe)
	a := evalWraparound(20_000_000, 200_000_000)
	if a != nil {
		t.Fatalf("expected nil for low age, got %+v", a)
	}
}

func TestEvalWraparound_At50Percent(t *testing.T) {
	// Exactly 50% → not > 0.50, so nil.
	a := evalWraparound(100_000_000, 200_000_000)
	if a != nil {
		t.Fatalf("expected nil at exactly 50%%, got %+v", a)
	}
}

func TestEvalWraparound_Above50Warn(t *testing.T) {
	// 51% → Warn
	a := evalWraparound(102_000_001, 200_000_000)
	if a == nil {
		t.Fatal("expected Warn advice above 50%, got nil")
	}
	if a.Level != pgtuner.Warn {
		t.Fatalf("expected Warn level, got %v", a.Level)
	}
}

func TestEvalWraparound_At80Percent(t *testing.T) {
	// Exactly 80% → not > 0.80, so Warn (not Bad).
	a := evalWraparound(160_000_000, 200_000_000)
	if a == nil {
		t.Fatal("expected advice at 80%, got nil")
	}
	if a.Level != pgtuner.Warn {
		t.Fatalf("expected Warn at exactly 80%%, got %v", a.Level)
	}
}

func TestEvalWraparound_Above80Bad(t *testing.T) {
	// 85% → Bad
	a := evalWraparound(170_000_001, 200_000_000)
	if a == nil {
		t.Fatal("expected Bad advice above 80%, got nil")
	}
	if a.Level != pgtuner.Bad {
		t.Fatalf("expected Bad level, got %v", a.Level)
	}
}

func TestEvalWraparound_ZeroMaxAge(t *testing.T) {
	// Zero maxAge → skip gracefully (avoid divide-by-zero).
	a := evalWraparound(1_000_000, 0)
	if a != nil {
		t.Fatalf("expected nil for zero maxAge, got %+v", a)
	}
}

// ----- evalWorkersCPU --------------------------------------------------------
// src: https://www.postgresql.org/docs/current/runtime-config-autovacuum.html#GUC-AUTOVACUUM-MAX-WORKERS

func TestEvalWorkersCPU_NoContention(t *testing.T) {
	// workers (3) == cpus (4) → no advice
	got := evalWorkersCPU("Autovacuum", 3, 4.0)
	if len(got) != 0 {
		t.Fatalf("expected no advice when workers <= cpus, got %v", levelsOf(got))
	}
}

func TestEvalWorkersCPU_EqualNoContention(t *testing.T) {
	// workers exactly equal to cpus → no advice
	got := evalWorkersCPU("Autovacuum", 4, 4.0)
	if len(got) != 0 {
		t.Fatalf("expected no advice when workers == cpus, got %v", levelsOf(got))
	}
}

func TestEvalWorkersCPU_ContentionInfo(t *testing.T) {
	// workers (8) > cpus (2) → Info
	got := evalWorkersCPU("Autovacuum", 8, 2.0)
	if len(got) == 0 || got[0].Level != pgtuner.Info {
		t.Fatalf("expected Info when workers > cpus, got %v", levelsOf(got))
	}
	if !strings.Contains(got[0].Message, "8") || !strings.Contains(got[0].Message, "2.0") {
		t.Fatalf("expected message to include worker count and CPU count, got: %s", got[0].Message)
	}
}

func TestEvalWorkersCPU_FractionalCPU(t *testing.T) {
	// Cgroup CPU quota = 0.5 (half a CPU), workers = 1 → contention
	got := evalWorkersCPU("Autovacuum", 1, 0.5)
	if len(got) == 0 || got[0].Level != pgtuner.Info {
		t.Fatalf("expected Info for workers=1 > fractional cpus=0.5, got %v", levelsOf(got))
	}
}
