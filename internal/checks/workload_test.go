package checks

import (
	"testing"

	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// ----- evalConnUtilization ---------------------------------------------------

// TestEvalConnUtilization_ZeroMax ensures no advice is emitted when max is 0
// (avoids division by zero; treated as unavailable data).
// src: https://www.postgresql.org/docs/current/runtime-config-connection.html#GUC-MAX-CONNECTIONS
func TestEvalConnUtilization_ZeroMax(t *testing.T) {
	got := evalConnUtilization(50, 0)
	if got != nil {
		t.Fatalf("expected nil for max=0, got %+v", got)
	}
}

// TestEvalConnUtilization_NegativeMax ensures no advice is emitted for
// impossible negative max values.
func TestEvalConnUtilization_NegativeMax(t *testing.T) {
	got := evalConnUtilization(10, -1)
	if got != nil {
		t.Fatalf("expected nil for negative max, got %+v", got)
	}
}

// TestEvalConnUtilization_BelowInfo verifies that utilization well below 60 %
// produces no advice.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
func TestEvalConnUtilization_BelowInfo(t *testing.T) {
	// 50/100 = 50 % — below the 60 % Info threshold.
	got := evalConnUtilization(50, 100)
	if got != nil {
		t.Fatalf("expected nil at 50%% utilization, got %+v", got)
	}
}

// TestEvalConnUtilization_AtInfoBoundary verifies the exact 60 % boundary
// produces no advice (the threshold is strictly >60 %).
func TestEvalConnUtilization_AtInfoBoundary(t *testing.T) {
	// 60/100 = exactly 60 % — not strictly > 60 %.
	got := evalConnUtilization(60, 100)
	if got != nil {
		t.Fatalf("expected nil at exactly 60%% utilization, got %+v", got)
	}
}

// TestEvalConnUtilization_AboveInfo verifies that utilization above 60 % but
// at or below 80 % produces an Info.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
func TestEvalConnUtilization_AboveInfo(t *testing.T) {
	// 70/100 = 70 % — above Info threshold but below Warn threshold.
	got := evalConnUtilization(70, 100)
	if got == nil {
		t.Fatal("expected Info advice at 70% utilization, got nil")
	}
	if got.Level != pgtuner.Info {
		t.Fatalf("expected Info level at 70%% utilization, got %s", got.Level)
	}
}

// TestEvalConnUtilization_AtWarnBoundary verifies the exact 80 % boundary
// produces no Warn (the threshold is strictly >80 %).
func TestEvalConnUtilization_AtWarnBoundary(t *testing.T) {
	// 80/100 = exactly 80 % — not strictly > 80 %.
	got := evalConnUtilization(80, 100)
	if got == nil {
		t.Fatal("expected Info advice at exactly 80% (below Warn threshold), got nil")
	}
	if got.Level != pgtuner.Info {
		t.Fatalf("expected Info at exactly 80%% (boundary), got %s", got.Level)
	}
}

// TestEvalConnUtilization_AboveWarn verifies that utilization above 80 %
// produces a Warn.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
func TestEvalConnUtilization_AboveWarn(t *testing.T) {
	// 90/100 = 90 % — above Warn threshold.
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

// TestEvalConnUtilization_Full verifies that 100 % utilization produces Warn.
func TestEvalConnUtilization_Full(t *testing.T) {
	got := evalConnUtilization(100, 100)
	if got == nil || got.Level != pgtuner.Warn {
		t.Fatalf("expected Warn at 100%% utilization, got %v", got)
	}
}

// TestEvalConnUtilization_MessageContainsSnapshot verifies that the message
// notes this is a point-in-time snapshot.
func TestEvalConnUtilization_MessageContainsSnapshot(t *testing.T) {
	got := evalConnUtilization(90, 100)
	if got == nil {
		t.Fatal("expected advice, got nil")
	}
	if !containsStr(got.Message, "point-in-time snapshot") {
		t.Errorf("expected 'point-in-time snapshot' in message, got: %s", got.Message)
	}
}

// ----- evalIdleInTransaction -------------------------------------------------

// TestEvalIdleInTransaction_ZeroCount verifies no advice when count is 0.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
func TestEvalIdleInTransaction_ZeroCount(t *testing.T) {
	got := evalIdleInTransaction(0, 0)
	if got != nil {
		t.Fatalf("expected nil for count=0, got %+v", got)
	}
}

// TestEvalIdleInTransaction_CountPositiveYoung verifies Info when count > 0
// but the oldest session is under the 300 s Warn threshold.
// src: https://www.postgresql.org/docs/current/runtime-config-client.html#GUC-IDLE-IN-TRANSACTION-SESSION-TIMEOUT
func TestEvalIdleInTransaction_CountPositiveYoung(t *testing.T) {
	// 1 session, 60 s old — below the 300 s Warn threshold.
	got := evalIdleInTransaction(1, 60.0)
	if got == nil {
		t.Fatal("expected Info advice for count=1, oldest=60s, got nil")
	}
	if got.Level != pgtuner.Info {
		t.Fatalf("expected Info for young idle-in-txn session, got %s", got.Level)
	}
}

// TestEvalIdleInTransaction_CountPositiveAtBoundary verifies that exactly 300 s
// does NOT trigger a Warn (the threshold is strictly > 300 s).
func TestEvalIdleInTransaction_CountPositiveAtBoundary(t *testing.T) {
	got := evalIdleInTransaction(1, 300.0)
	if got == nil {
		t.Fatal("expected Info advice at exactly 300s boundary, got nil")
	}
	if got.Level != pgtuner.Info {
		t.Fatalf("expected Info at exactly 300s boundary, got %s", got.Level)
	}
}

// TestEvalIdleInTransaction_CountPositiveOld verifies Warn when count > 0
// and the oldest session exceeds the 300 s threshold.
// src: https://www.postgresql.org/docs/current/runtime-config-client.html#GUC-IDLE-IN-TRANSACTION-SESSION-TIMEOUT
func TestEvalIdleInTransaction_CountPositiveOld(t *testing.T) {
	// 3 sessions, oldest is 600 s — above the 300 s Warn threshold.
	got := evalIdleInTransaction(3, 600.0)
	if got == nil {
		t.Fatal("expected Warn advice for count=3, oldest=600s, got nil")
	}
	if got.Level != pgtuner.Warn {
		t.Fatalf("expected Warn for old idle-in-txn session, got %s", got.Level)
	}
}

// TestEvalIdleInTransaction_MessageContainsSnapshot verifies that the advice
// message notes the point-in-time nature of the snapshot.
func TestEvalIdleInTransaction_MessageContainsSnapshot(t *testing.T) {
	got := evalIdleInTransaction(1, 600.0)
	if got == nil {
		t.Fatal("expected advice, got nil")
	}
	if !containsStr(got.Message, "point-in-time snapshot") {
		t.Errorf("expected 'point-in-time snapshot' in message, got: %s", got.Message)
	}
}

// TestEvalIdleInTransaction_WarnMessageContainsLocks verifies that the Warn
// message mentions locks and autovacuum.
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

// TestEvalIdleInTransaction_AbortedStateIncluded verifies that the advice
// message acknowledges both 'idle in transaction' and 'idle in transaction
// (aborted)' states, since both hold locks and block vacuum.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
func TestEvalIdleInTransaction_AbortedStateIncluded(t *testing.T) {
	// The Info path (young session).
	got := evalIdleInTransaction(1, 10.0)
	if got == nil {
		t.Fatal("expected Info advice, got nil")
	}
	if !containsStr(got.Message, "aborted") {
		t.Errorf("expected 'aborted' in Info message to reflect both states, got: %s", got.Message)
	}

	// The Warn path (old session).
	gotWarn := evalIdleInTransaction(1, 400.0)
	if gotWarn == nil || gotWarn.Level != pgtuner.Warn {
		t.Fatal("expected Warn advice for old session")
	}
	if !containsStr(gotWarn.Message, "aborted") {
		t.Errorf("expected 'aborted' in Warn message to reflect both states, got: %s", gotWarn.Message)
	}
}

// ----- evalLongRunningQueries ------------------------------------------------

// TestEvalLongRunningQueries_ZeroCount verifies no advice when count is 0.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
func TestEvalLongRunningQueries_ZeroCount(t *testing.T) {
	got := evalLongRunningQueries(0, 0)
	if got != nil {
		t.Fatalf("expected nil for count=0, got %+v", got)
	}
}

// TestEvalLongRunningQueries_BelowThreshold verifies no advice when count > 0
// but the oldest query is under the threshold.
func TestEvalLongRunningQueries_BelowThreshold(t *testing.T) {
	// 2 active queries, oldest is 100 s — below the 300 s Info threshold.
	got := evalLongRunningQueries(2, 100.0)
	if got != nil {
		t.Fatalf("expected nil for oldest=100s (below threshold), got %+v", got)
	}
}

// TestEvalLongRunningQueries_AtBoundary verifies that exactly 300 s does NOT
// produce Info (the threshold is strictly > 300 s).
func TestEvalLongRunningQueries_AtBoundary(t *testing.T) {
	got := evalLongRunningQueries(1, 300.0)
	if got != nil {
		t.Fatalf("expected nil at exactly 300s boundary, got %+v", got)
	}
}

// TestEvalLongRunningQueries_AboveThreshold verifies Info when the oldest
// active query exceeds the 300 s threshold.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
func TestEvalLongRunningQueries_AboveThreshold(t *testing.T) {
	// 1 active query running for 600 s — above the threshold.
	got := evalLongRunningQueries(1, 600.0)
	if got == nil {
		t.Fatal("expected Info advice for oldest=600s, got nil")
	}
	if got.Level != pgtuner.Info {
		t.Fatalf("expected Info level, got %s", got.Level)
	}
}

// TestEvalLongRunningQueries_MessageContainsSnapshot verifies the snapshot note.
func TestEvalLongRunningQueries_MessageContainsSnapshot(t *testing.T) {
	got := evalLongRunningQueries(1, 600.0)
	if got == nil {
		t.Fatal("expected advice, got nil")
	}
	if !containsStr(got.Message, "point-in-time snapshot") {
		t.Errorf("expected 'point-in-time snapshot' in message, got: %s", got.Message)
	}
}

// TestEvalLongRunningQueries_MessageContainsCount verifies the count appears
// in the message so operators know how many queries are affected.
func TestEvalLongRunningQueries_MessageContainsCount(t *testing.T) {
	got := evalLongRunningQueries(5, 700.0)
	if got == nil {
		t.Fatal("expected advice, got nil")
	}
	if !containsStr(got.Message, "5") {
		t.Errorf("expected count '5' in message, got: %s", got.Message)
	}
}

// ----- evalBlockedSessions ---------------------------------------------------

// TestEvalBlockedSessions_Zero verifies no advice when count is 0.
// src: https://www.postgresql.org/docs/current/functions-info.html#FUNCTIONS-PG-BLOCKING-PIDS
func TestEvalBlockedSessions_Zero(t *testing.T) {
	got := evalBlockedSessions(0)
	if got != nil {
		t.Fatalf("expected nil for count=0, got %+v", got)
	}
}

// TestEvalBlockedSessions_One verifies Warn for a single blocked session.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
func TestEvalBlockedSessions_One(t *testing.T) {
	got := evalBlockedSessions(1)
	if got == nil {
		t.Fatal("expected Warn for 1 blocked session, got nil")
	}
	if got.Level != pgtuner.Warn {
		t.Fatalf("expected Warn level, got %s", got.Level)
	}
}

// TestEvalBlockedSessions_Many verifies Warn for multiple blocked sessions and
// that the count appears in the message.
func TestEvalBlockedSessions_Many(t *testing.T) {
	got := evalBlockedSessions(7)
	if got == nil || got.Level != pgtuner.Warn {
		t.Fatalf("expected Warn for 7 blocked sessions, got %v", got)
	}
	if !containsStr(got.Message, "7") {
		t.Errorf("expected count '7' in message, got: %s", got.Message)
	}
}

// TestEvalBlockedSessions_MessageContainsSnapshot verifies the snapshot note.
func TestEvalBlockedSessions_MessageContainsSnapshot(t *testing.T) {
	got := evalBlockedSessions(2)
	if got == nil {
		t.Fatal("expected advice, got nil")
	}
	if !containsStr(got.Message, "point-in-time snapshot") {
		t.Errorf("expected 'point-in-time snapshot' in message, got: %s", got.Message)
	}
}

// ----- evalDanglingPreparedXacts ---------------------------------------------

// TestEvalDanglingPreparedXacts_Zero verifies no advice when count is 0.
// src: https://www.postgresql.org/docs/current/view-pg-prepared-xacts.html
func TestEvalDanglingPreparedXacts_Zero(t *testing.T) {
	got := evalDanglingPreparedXacts(0, 0)
	if got != nil {
		t.Fatalf("expected nil for count=0, got %+v", got)
	}
}

// TestEvalDanglingPreparedXacts_One verifies Warn for a single dangling
// prepared transaction, regardless of age.
// src: https://www.postgresql.org/docs/current/view-pg-prepared-xacts.html
func TestEvalDanglingPreparedXacts_One(t *testing.T) {
	got := evalDanglingPreparedXacts(1, 10.0)
	if got == nil {
		t.Fatal("expected Warn for 1 dangling prepared transaction, got nil")
	}
	if got.Level != pgtuner.Warn {
		t.Fatalf("expected Warn level, got %s", got.Level)
	}
}

// TestEvalDanglingPreparedXacts_Many verifies Warn for multiple dangling
// transactions and that count appears in the message.
func TestEvalDanglingPreparedXacts_Many(t *testing.T) {
	got := evalDanglingPreparedXacts(4, 3600.0)
	if got == nil || got.Level != pgtuner.Warn {
		t.Fatalf("expected Warn for 4 dangling prepared xacts, got %v", got)
	}
	if !containsStr(got.Message, "4") {
		t.Errorf("expected count '4' in message, got: %s", got.Message)
	}
}

// TestEvalDanglingPreparedXacts_MessageContainsSnapshot verifies the snapshot note.
func TestEvalDanglingPreparedXacts_MessageContainsSnapshot(t *testing.T) {
	got := evalDanglingPreparedXacts(1, 120.0)
	if got == nil {
		t.Fatal("expected advice, got nil")
	}
	if !containsStr(got.Message, "point-in-time snapshot") {
		t.Errorf("expected 'point-in-time snapshot' in message, got: %s", got.Message)
	}
}

// TestEvalDanglingPreparedXacts_MessageContainsVacuum verifies that vacuum
// risk is mentioned in the Warn message.
func TestEvalDanglingPreparedXacts_MessageContainsVacuum(t *testing.T) {
	got := evalDanglingPreparedXacts(1, 60.0)
	if got == nil {
		t.Fatal("expected Warn advice")
	}
	if !containsStr(got.Message, "autovacuum") {
		t.Errorf("expected 'autovacuum' in message, got: %s", got.Message)
	}
}
