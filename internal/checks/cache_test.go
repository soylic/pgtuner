package checks

import (
	"testing"

	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// TestCacheHitRatioZeroActivity: no block accesses → Info (not Warn/Bad).
func TestCacheHitRatioZeroActivity(t *testing.T) {
	got := evalCacheHitRatio(0, 0)
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for zero buffer activity, got %v", levelsOf(got))
	}
	for _, a := range got {
		if a.Level == pgtuner.Warn || a.Level == pgtuner.Bad {
			t.Fatalf("unexpected high-severity for zero buffer activity: %v", a)
		}
	}
}

// TestCacheHitRatioLow: ratio 0.985 (< 0.99) → Warn.
func TestCacheHitRatioLow(t *testing.T) {
	// 985 hits, 15 reads → ratio = 0.985
	got := evalCacheHitRatio(985, 15)
	if !hasLevel(got, pgtuner.Warn) {
		t.Fatalf("expected Warn for cache hit ratio 0.985, got %v", levelsOf(got))
	}
}

// TestCacheHitRatioExactBoundary: ratio exactly 0.99 → OK (not Warn; threshold is strict <).
func TestCacheHitRatioExactBoundary(t *testing.T) {
	// 99 hits, 1 read → ratio = 0.99 exactly; should NOT warn (< not <=)
	got := evalCacheHitRatio(99, 1)
	for _, a := range got {
		if a.Level == pgtuner.Warn {
			t.Fatalf("ratio exactly 0.99 should NOT trigger Warn (threshold is < 0.99), got %v", got)
		}
	}
}

// TestCacheHitRatioHealthy: ratio 0.999 → no advice (OK path via Run; pure fn returns nil).
func TestCacheHitRatioHealthy(t *testing.T) {
	// 999 hits, 1 read → ratio = 0.999
	got := evalCacheHitRatio(999, 1)
	// Pure fn returns nil when healthy; Run wraps nil into OK.
	for _, a := range got {
		if a.Level == pgtuner.Warn || a.Level == pgtuner.Bad {
			t.Fatalf("unexpected severity for healthy ratio 0.999: %v", a)
		}
	}
}

// TestIndexHitRatioNullHit: NULL SUM(idx_blks_hit) → no advice.
func TestIndexHitRatioNullHit(t *testing.T) {
	got := evalIndexHitRatio(nil, nil)
	if len(got) != 0 {
		t.Fatalf("expected no advice for NULL index stats (no user indexes), got %v", got)
	}
}

// TestIndexHitRatioZeroAccess: both sums zero → no advice.
func TestIndexHitRatioZeroAccess(t *testing.T) {
	hit := int64(0)
	read := int64(0)
	got := evalIndexHitRatio(&hit, &read)
	if len(got) != 0 {
		t.Fatalf("expected no advice for zero index access, got %v", got)
	}
}

// TestIndexHitRatioLow: ratio 0.90 (< 0.95) → Info.
func TestIndexHitRatioLow(t *testing.T) {
	hit := int64(90)
	read := int64(10)
	got := evalIndexHitRatio(&hit, &read)
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for index hit ratio 0.90, got %v", levelsOf(got))
	}
}

// TestIndexHitRatioHighEnough: ratio 0.98 (≥ 0.95) → no advice.
func TestIndexHitRatioHighEnough(t *testing.T) {
	hit := int64(98)
	read := int64(2)
	got := evalIndexHitRatio(&hit, &read)
	for _, a := range got {
		if a.Level == pgtuner.Info || a.Level == pgtuner.Warn {
			t.Fatalf("expected no advice for index hit ratio 0.98, got %v", got)
		}
	}
}

// TestIndexHitRatioExactBoundary: ratio exactly 0.95 → no advice (threshold is strict <).
func TestIndexHitRatioExactBoundary(t *testing.T) {
	hit := int64(95)
	read := int64(5)
	got := evalIndexHitRatio(&hit, &read)
	for _, a := range got {
		if a.Level == pgtuner.Info {
			t.Fatalf("ratio exactly 0.95 should NOT trigger Info (threshold is < 0.95), got %v", got)
		}
	}
}

// TestTableHitRatioNullHit: NULL SUM(heap_blks_hit) → no advice.
func TestTableHitRatioNullHit(t *testing.T) {
	got := evalTableHitRatio(nil, nil)
	if len(got) != 0 {
		t.Fatalf("expected no advice for NULL table stats (no user tables), got %v", got)
	}
}

// TestTableHitRatioZeroAccess: both sums zero → no advice.
func TestTableHitRatioZeroAccess(t *testing.T) {
	hit := int64(0)
	read := int64(0)
	got := evalTableHitRatio(&hit, &read)
	if len(got) != 0 {
		t.Fatalf("expected no advice for zero table access, got %v", got)
	}
}

// TestTableHitRatioLow: ratio 0.90 (< 0.95) → Info.
func TestTableHitRatioLow(t *testing.T) {
	hit := int64(90)
	read := int64(10)
	got := evalTableHitRatio(&hit, &read)
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for table hit ratio 0.90, got %v", levelsOf(got))
	}
}

// TestTableHitRatioExactBoundary: ratio exactly 0.95 → no advice (threshold is strict <).
func TestTableHitRatioExactBoundary(t *testing.T) {
	hit := int64(95)
	read := int64(5)
	got := evalTableHitRatio(&hit, &read)
	for _, a := range got {
		if a.Level == pgtuner.Info {
			t.Fatalf("ratio exactly 0.95 should NOT trigger Info (threshold is < 0.95), got %v", got)
		}
	}
}

// TestTableHitRatioHighEnough: ratio 0.98 (≥ 0.95) → no advice.
func TestTableHitRatioHighEnough(t *testing.T) {
	hit := int64(98)
	read := int64(2)
	got := evalTableHitRatio(&hit, &read)
	for _, a := range got {
		if a.Level == pgtuner.Info || a.Level == pgtuner.Warn {
			t.Fatalf("expected no advice for table hit ratio 0.98, got %v", got)
		}
	}
}
