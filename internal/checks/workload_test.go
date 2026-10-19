package checks

import (
	"testing"

	"github.com/soylic/pgtuner/pkg/pgtuner"
)

func TestEvalConnUtilization_ZeroMax(t *testing.T) {
	got := evalConnUtilization(50, 0)
	if got != nil {
		t.Fatalf("expected nil for max=0, got %+v", got)
	}
}

func TestEvalConnUtilization_NegativeMax(t *testing.T) {
	got := evalConnUtilization(10, -1)
	if got != nil {
		t.Fatalf("expected nil for negative max, got %+v", got)
	}
}

func TestEvalConnUtilization_BelowInfo(t *testing.T) {
	got := evalConnUtilization(50, 100)
	if got != nil {
		t.Fatalf("expected nil at 50%% utilization, got %+v", got)
	}
}

func TestEvalConnUtilization_AtInfoBoundary(t *testing.T) {
	got := evalConnUtilization(60, 100)
	if got != nil {
		t.Fatalf("expected nil at exactly 60%% utilization, got %+v", got)
	}
}

func TestEvalConnUtilization_AboveInfo(t *testing.T) {
	got := evalConnUtilization(70, 100)
	if got == nil {
		t.Fatal("expected Info advice at 70% utilization, got nil")
	}
	if got.Level != pgtuner.Info {
		t.Fatalf("expected Info level at 70%% utilization, got %s", got.Level)
	}
}

func TestEvalConnUtilization_AtWarnBoundary(t *testing.T) {
	got := evalConnUtilization(80, 100)
	if got == nil {
		t.Fatal("expected Info advice at exactly 80% (below Warn threshold), got nil")
	}
	if got.Level != pgtuner.Info {
		t.Fatalf("expected Info at exactly 80%% (boundary), got %s", got.Level)
	}
}

func TestEvalConnUtilization_AboveWarn(t *testing.T) {
	got := evalConnUtilization(90, 100)
	if got == nil {
		t.Fatal("expected Warn advice at 90% utilization, got nil")
	}
	if got.Level != pgtuner.Warn {
		t.Fatalf("expected Warn level at 90%% utilization, got %s", got.Level)
	}
	if got.Message == "" || got.Title == "" {
		t.Fatal("expected non-empty title and message in Warn advice")
	}
}

func TestEvalConnUtilization_Full(t *testing.T) {
	got := evalConnUtilization(100, 100)
	if got == nil || got.Level != pgtuner.Warn {
		t.Fatalf("expected Warn at 100%% utilization, got %v", got)
	}
}

func TestEvalConnUtilization_MessageContainsSnapshot(t *testing.T) {
	got := evalConnUtilization(90, 100)
	if got == nil {
		t.Fatal("expected advice, got nil")
	}
	if !containsStr(got.Message, "point-in-time snapshot") {
		t.Errorf("expected 'point-in-time snapshot' in message, got: %s", got.Message)
	}
}

func TestEvalIdleInTransaction_ZeroCount(t *testing.T) {
	got := evalIdleInTransaction(0, 0)
	if got != nil {
		t.Fatalf("expected nil for count=0, got %+v", got)
	}
}

func TestEvalIdleInTransaction_CountPositiveYoung(t *testing.T) {
	got := evalIdleInTransaction(1, 60.0)
	if got == nil {
		t.Fatal("expected Info advice for count=1, oldest=60s, got nil")
	}
	if got.Level != pgtuner.Info {
		t.Fatalf("expected Info for young idle-in-txn session, got %s", got.Level)
	}
}

func TestEvalIdleInTransaction_CountPositiveAtBoundary(t *testing.T) {
	got := evalIdleInTransaction(1, 300.0)
	if got == nil {
		t.Fatal("expected Info advice at exactly 300s boundary, got nil")
	}
	if got.Level != pgtuner.Info {
		t.Fatalf("expected Info at exactly 300s boundary, got %s", got.Level)
	}
}

func TestEvalIdleInTransaction_CountPositiveOld(t *testing.T) {
	got := evalIdleInTransaction(3, 600.0)
	if got == nil {
		t.Fatal("expected Warn advice for count=3, oldest=600s, got nil")
	}
	if got.Level != pgtuner.Warn {
		t.Fatalf("expected Warn for old idle-in-txn session, got %s", got.Level)
	}
}

func TestEvalIdleInTransaction_MessageContainsSnapshot(t *testing.T) {
	got := evalIdleInTransaction(1, 600.0)
	if got == nil {
		t.Fatal("expected advice, got nil")
	}
	if !containsStr(got.Message, "point-in-time snapshot") {
		t.Errorf("expected 'point-in-time snapshot' in message, got: %s", got.Message)
	}
}

func TestEvalIdleInTransaction_WarnMessageContainsLocks(t *testing.T) {
	got := evalIdleInTransaction(2, 400.0)
	if got == nil || got.Level != pgtuner.Warn {
		t.Fatal("expected Warn advice")
	}
	if !containsStr(got.Message, "locks") {
		t.Errorf("expected 'locks' in Warn message, got: %s", got.Message)
	}
	if !containsStr(got.Message, "autovacuum") {
		t.Errorf("expected 'autovacuum' in Warn message, got: %s", got.Message)
	}
}

func TestEvalIdleInTransaction_AbortedStateIncluded(t *testing.T) {
	got := evalIdleInTransaction(1, 10.0)
	if got == nil {
		t.Fatal("expected Info advice, got nil")
	}
	if !containsStr(got.Message, "aborted") {
		t.Errorf("expected 'aborted' in Info message to reflect both states, got: %s", got.Message)
	}

	gotWarn := evalIdleInTransaction(1, 400.0)
	if gotWarn == nil || gotWarn.Level != pgtuner.Warn {
		t.Fatal("expected Warn advice for old session")
	}
	if !containsStr(gotWarn.Message, "aborted") {
		t.Errorf("expected 'aborted' in Warn message to reflect both states, got: %s", gotWarn.Message)
	}
}

func TestEvalLongRunningQueries_ZeroCount(t *testing.T) {
	got := evalLongRunningQueries(0, 0)
	if got != nil {
		t.Fatalf("expected nil for count=0, got %+v", got)
	}
}

func TestEvalLongRunningQueries_BelowThreshold(t *testing.T) {
	got := evalLongRunningQueries(2, 100.0)
	if got != nil {
		t.Fatalf("expected nil for oldest=100s (below threshold), got %+v", got)
	}
}

func TestEvalLongRunningQueries_AtBoundary(t *testing.T) {
	got := evalLongRunningQueries(1, 300.0)
	if got != nil {
		t.Fatalf("expected nil at exactly 300s boundary, got %+v", got)
	}
}

func TestEvalLongRunningQueries_AboveThreshold(t *testing.T) {
	got := evalLongRunningQueries(1, 600.0)
	if got == nil {
		t.Fatal("expected Info advice for oldest=600s, got nil")
	}
	if got.Level != pgtuner.Info {
		t.Fatalf("expected Info level, got %s", got.Level)
	}
}

func TestEvalLongRunningQueries_MessageContainsSnapshot(t *testing.T) {
	got := evalLongRunningQueries(1, 600.0)
	if got == nil {
		t.Fatal("expected advice, got nil")
	}
	if !containsStr(got.Message, "point-in-time snapshot") {
		t.Errorf("expected 'point-in-time snapshot' in message, got: %s", got.Message)
	}
}

func TestEvalLongRunningQueries_MessageContainsCount(t *testing.T) {
	got := evalLongRunningQueries(5, 700.0)
	if got == nil {
		t.Fatal("expected advice, got nil")
	}
	if !containsStr(got.Message, "5") {
		t.Errorf("expected count '5' in message, got: %s", got.Message)
	}
}

func TestEvalBlockedSessions_Zero(t *testing.T) {
	got := evalBlockedSessions(0)
	if got != nil {
		t.Fatalf("expected nil for count=0, got %+v", got)
	}
}

func TestEvalBlockedSessions_One(t *testing.T) {
	got := evalBlockedSessions(1)
	if got == nil {
		t.Fatal("expected Warn for 1 blocked session, got nil")
	}
	if got.Level != pgtuner.Warn {
		t.Fatalf("expected Warn level, got %s", got.Level)
	}
}

func TestEvalBlockedSessions_Many(t *testing.T) {
	got := evalBlockedSessions(7)
	if got == nil || got.Level != pgtuner.Warn {
		t.Fatalf("expected Warn for 7 blocked sessions, got %v", got)
	}
	if !containsStr(got.Message, "7") {
		t.Errorf("expected count '7' in message, got: %s", got.Message)
	}
}

func TestEvalBlockedSessions_MessageContainsSnapshot(t *testing.T) {
	got := evalBlockedSessions(2)
	if got == nil {
		t.Fatal("expected advice, got nil")
	}
	if !containsStr(got.Message, "point-in-time snapshot") {
		t.Errorf("expected 'point-in-time snapshot' in message, got: %s", got.Message)
	}
}

func TestEvalDanglingPreparedXacts_Zero(t *testing.T) {
	got := evalDanglingPreparedXacts(0, 0)
	if got != nil {
		t.Fatalf("expected nil for count=0, got %+v", got)
	}
}

func TestEvalDanglingPreparedXacts_One(t *testing.T) {
	got := evalDanglingPreparedXacts(1, 10.0)
	if got == nil {
		t.Fatal("expected Warn for 1 dangling prepared transaction, got nil")
	}
	if got.Level != pgtuner.Warn {
		t.Fatalf("expected Warn level, got %s", got.Level)
	}
}

func TestEvalDanglingPreparedXacts_Many(t *testing.T) {
	got := evalDanglingPreparedXacts(4, 3600.0)
	if got == nil || got.Level != pgtuner.Warn {
		t.Fatalf("expected Warn for 4 dangling prepared xacts, got %v", got)
	}
	if !containsStr(got.Message, "4") {
		t.Errorf("expected count '4' in message, got: %s", got.Message)
	}
}

func TestEvalDanglingPreparedXacts_MessageContainsSnapshot(t *testing.T) {
	got := evalDanglingPreparedXacts(1, 120.0)
	if got == nil {
		t.Fatal("expected advice, got nil")
	}
	if !containsStr(got.Message, "point-in-time snapshot") {
		t.Errorf("expected 'point-in-time snapshot' in message, got: %s", got.Message)
	}
}

func TestEvalDanglingPreparedXacts_MessageContainsVacuum(t *testing.T) {
	got := evalDanglingPreparedXacts(1, 60.0)
	if got == nil {
		t.Fatal("expected Warn advice")
	}
	if !containsStr(got.Message, "autovacuum") {
		t.Errorf("expected 'autovacuum' in message, got: %s", got.Message)
	}
}
