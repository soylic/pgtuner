package checks

import (
	"testing"

	"github.com/soylic/pgtuner/internal/pg"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// TestCheckpointQueryPG16: PG16 → bgwriter SQL.
func TestCheckpointQueryPG16(t *testing.T) {
	q := checkpointQuery(pg.ServerVersion{Major: 16, Minor: 0})
	if q != "SELECT checkpoints_timed, checkpoints_req FROM pg_stat_bgwriter" {
		t.Fatalf("PG16 expected bgwriter query, got: %q", q)
	}
}

// TestCheckpointQueryPG17: PG17 → checkpointer SQL.
func TestCheckpointQueryPG17(t *testing.T) {
	q := checkpointQuery(pg.ServerVersion{Major: 17, Minor: 0})
	if q != "SELECT num_timed, num_requested FROM pg_stat_checkpointer" {
		t.Fatalf("PG17 expected checkpointer query, got: %q", q)
	}
}

// TestCheckpointQueryPG14: PG14 → bgwriter SQL (below 17).
func TestCheckpointQueryPG14(t *testing.T) {
	q := checkpointQuery(pg.ServerVersion{Major: 14, Minor: 5})
	if q != "SELECT checkpoints_timed, checkpoints_req FROM pg_stat_bgwriter" {
		t.Fatalf("PG14 expected bgwriter query, got: %q", q)
	}
}

// TestCheckpointQueryPG18: PG18 → checkpointer SQL (above 17).
func TestCheckpointQueryPG18(t *testing.T) {
	q := checkpointQuery(pg.ServerVersion{Major: 18, Minor: 0})
	if q != "SELECT num_timed, num_requested FROM pg_stat_checkpointer" {
		t.Fatalf("PG18 expected checkpointer query, got: %q", q)
	}
}

// TestEvalCheckpointsZeroActivity: no checkpoints → Info.
func TestEvalCheckpointsZeroActivity(t *testing.T) {
	got := evalCheckpoints(0, 0, 0.9)
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for zero checkpoint activity, got %v", levelsOf(got))
	}
	// Must not be Warn or Bad.
	for _, a := range got {
		if a.Level == pgtuner.Warn || a.Level == pgtuner.Bad {
			t.Fatalf("unexpected high-severity for zero activity: %v", a)
		}
	}
}

// TestEvalCheckpointsHighRequestedRatio: >30% requested → Warn.
func TestEvalCheckpointsHighRequestedRatio(t *testing.T) {
	// 40 requested, 60 timed → ratio = 40/100 = 0.40 > 0.30
	got := evalCheckpoints(60, 40, 0.9)
	if !hasLevel(got, pgtuner.Warn) {
		t.Fatalf("expected Warn for high requested-checkpoint ratio, got %v", levelsOf(got))
	}
}

// TestEvalCheckpointsLowRequestedRatio: ≤30% requested → no Warn from ratio rule.
func TestEvalCheckpointsLowRequestedRatio(t *testing.T) {
	// 20 requested, 80 timed → ratio = 0.20 ≤ 0.30
	got := evalCheckpoints(80, 20, 0.9)
	for _, a := range got {
		if a.Level == pgtuner.Warn || a.Level == pgtuner.Bad {
			t.Fatalf("unexpected Warn/Bad for acceptable requested-checkpoint ratio: %v", a)
		}
	}
}

// TestEvalCheckpointsLowCompletionTarget: completion_target < 0.9 → Info.
func TestEvalCheckpointsLowCompletionTarget(t *testing.T) {
	got := evalCheckpoints(100, 5, 0.5)
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for low checkpoint_completion_target, got %v", levelsOf(got))
	}
}

// TestEvalCheckpointsHighRequestedAndLowTarget: both rules fire → Warn + Info.
func TestEvalCheckpointsHighRequestedAndLowTarget(t *testing.T) {
	// 50 requested, 50 timed → ratio = 0.50; completion target = 0.5
	got := evalCheckpoints(50, 50, 0.5)
	if !hasLevel(got, pgtuner.Warn) {
		t.Fatalf("expected Warn from high requested ratio, got %v", levelsOf(got))
	}
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info from low completion target, got %v", levelsOf(got))
	}
}

// TestEvalCheckpointsHealthy: good counters + 0.9 target → OK.
func TestEvalCheckpointsHealthy(t *testing.T) {
	// 1 requested out of 100 total = 1%, well under threshold.
	got := evalCheckpoints(99, 1, 0.9)
	if !hasLevel(got, pgtuner.OK) {
		t.Fatalf("expected OK for healthy checkpoint stats, got %v", levelsOf(got))
	}
}

// TestEvalCheckpointsExactThreshold: exactly 30% → no Warn (boundary is exclusive >0.30).
func TestEvalCheckpointsExactThreshold(t *testing.T) {
	// 30 requested, 70 timed → ratio = 0.30, not > 0.30
	got := evalCheckpoints(70, 30, 0.9)
	for _, a := range got {
		if a.Level == pgtuner.Warn {
			t.Fatalf("30%% exactly should NOT trigger Warn (threshold is > 0.30), got %v", got)
		}
	}
}
