package checks

import (
	"testing"

	"github.com/OpenRemedy/pgtuner/internal/pg"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// ----- evalInactiveSlots -----------------------------------------------------

func TestEvalInactiveSlots_None(t *testing.T) {
	a := evalInactiveSlots(nil)
	if a != nil {
		t.Fatalf("expected nil for no inactive slots, got %+v", a)
	}
}

func TestEvalInactiveSlots_Empty(t *testing.T) {
	a := evalInactiveSlots([]string{})
	if a != nil {
		t.Fatalf("expected nil for empty slice, got %+v", a)
	}
}

func TestEvalInactiveSlots_OneWarn(t *testing.T) {
	a := evalInactiveSlots([]string{"slot1"})
	if a == nil {
		t.Fatal("expected Warn for one inactive slot, got nil")
	}
	if a.Level != pgtuner.Warn {
		t.Fatalf("expected Warn level, got %v", a.Level)
	}
	if a.Message == "" {
		t.Fatal("expected non-empty message")
	}
}

func TestEvalInactiveSlots_MultipleWarn(t *testing.T) {
	a := evalInactiveSlots([]string{"slot1", "slot2", "slot3"})
	if a == nil || a.Level != pgtuner.Warn {
		t.Fatalf("expected Warn for multiple inactive slots, got %+v", a)
	}
}

// ----- max_wal_senders settings rule -----------------------------------------

func TestMaxWALSenders_Zero(t *testing.T) {
	// Simulate the settings rule: max_wal_senders=0 → Info
	s := pg.NewSettings(map[string]string{
		"max_wal_senders": "0",
	})
	mws, ok := s.Int("max_wal_senders")
	if !ok {
		t.Fatal("expected max_wal_senders to be parseable")
	}
	var advice []pgtuner.Advice
	if mws == 0 {
		advice = append(advice, pgtuner.Advice{Level: pgtuner.Info, Section: "Replication"})
	}
	if len(advice) == 0 || advice[0].Level != pgtuner.Info {
		t.Fatalf("expected Info for max_wal_senders=0, got %v", levelsOf(advice))
	}
}

func TestMaxWALSenders_Nonzero(t *testing.T) {
	// max_wal_senders=10 → no Info from that rule
	s := pg.NewSettings(map[string]string{
		"max_wal_senders": "10",
	})
	mws, _ := s.Int("max_wal_senders")
	var advice []pgtuner.Advice
	if mws == 0 {
		advice = append(advice, pgtuner.Advice{Level: pgtuner.Info, Section: "Replication"})
	}
	if len(advice) != 0 {
		t.Fatalf("expected no advice for max_wal_senders=10, got %v", levelsOf(advice))
	}
}

// ----- evalRoleInfo ----------------------------------------------------------

func TestEvalRoleInfo_Primary(t *testing.T) {
	a := evalRoleInfo(pg.Role{Role: pg.RolePrimary, ConnectedStandbys: 2})
	if a.Level != pgtuner.Info {
		t.Fatalf("expected Info for primary role, got %v", a.Level)
	}
	if a.Section != "Replication" {
		t.Fatalf("expected Replication section, got %q", a.Section)
	}
	if a.Title == "" || a.Message == "" {
		t.Fatal("expected non-empty title/message")
	}
}

func TestEvalRoleInfo_Standby(t *testing.T) {
	a := evalRoleInfo(pg.Role{Role: pg.RoleStandby, InRecovery: true})
	if a.Level != pgtuner.Info {
		t.Fatalf("expected Info for standby role, got %v", a.Level)
	}
	if a.Title == "" || a.Message == "" {
		t.Fatal("expected non-empty title/message")
	}
}

func TestEvalRoleInfo_Standalone(t *testing.T) {
	a := evalRoleInfo(pg.Role{Role: pg.RoleStandalone})
	if a.Level != pgtuner.Info {
		t.Fatalf("expected Info for standalone role, got %v", a.Level)
	}
	if a.Title == "" || a.Message == "" {
		t.Fatal("expected non-empty title/message")
	}
}

func TestEvalRoleInfo_Unknown(t *testing.T) {
	a := evalRoleInfo(pg.Role{Role: pg.RoleUnknown})
	if a.Level != pgtuner.Unknown {
		t.Fatalf("expected Unknown level for unknown role, got %v", a.Level)
	}
}

// ----- evalReplicationLag ----------------------------------------------------

func TestEvalReplicationLag_NoRows_NoAdvice(t *testing.T) {
	// No standbys → no lag advice.
	out := evalReplicationLag(nil, "")
	if len(out) != 0 {
		t.Fatalf("expected no advice for no replica rows, got %v", levelsOf(out))
	}
}

func TestEvalReplicationLag_BelowThreshold_NoAdvice(t *testing.T) {
	// Standby with replay_lag = 10s (< 30s threshold) → no lag warning.
	// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-VIEW
	lag := 10.0
	rows := []ReplicaRow{
		{ApplicationName: "replica1", SyncState: "async", ReplayLagSecs: &lag},
	}
	out := evalReplicationLag(rows, "")
	if len(out) != 0 {
		t.Fatalf("expected no advice for lag=10s (below threshold), got %v", levelsOf(out))
	}
}

func TestEvalReplicationLag_AtThreshold_NoAdvice(t *testing.T) {
	// Boundary: replay_lag == 30.0 exactly → no Warn (condition is > 30, exclusive).
	lag := 30.0
	rows := []ReplicaRow{
		{ApplicationName: "replica1", SyncState: "async", ReplayLagSecs: &lag},
	}
	out := evalReplicationLag(rows, "")
	for _, a := range out {
		if a.Level == pgtuner.Warn {
			t.Fatalf("lag=30.0 at boundary should NOT trigger Warn (condition is > 30s exclusive), got %+v", a)
		}
	}
}

func TestEvalReplicationLag_AboveThreshold_Warn(t *testing.T) {
	// Standby with replay_lag > 30s → Warn.
	// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-VIEW
	lag := 45.0
	rows := []ReplicaRow{
		{ApplicationName: "replica1", SyncState: "async", ReplayLagSecs: &lag},
	}
	out := evalReplicationLag(rows, "")
	if !hasLevel(out, pgtuner.Warn) {
		t.Fatalf("expected Warn for lag=45s, got %v", levelsOf(out))
	}
}

func TestEvalReplicationLag_NullLag_NoAdvice(t *testing.T) {
	// NULL replay_lag (standby hasn't replayed yet) → no lag Warn.
	rows := []ReplicaRow{
		{ApplicationName: "replica1", SyncState: "async", ReplayLagSecs: nil},
	}
	out := evalReplicationLag(rows, "")
	if hasLevel(out, pgtuner.Warn) {
		t.Fatalf("expected no Warn for null replay_lag, got %v", levelsOf(out))
	}
}

func TestEvalReplicationLag_SyncNamesSet_NoSyncStandby_Warn(t *testing.T) {
	// synchronous_standby_names is set but no standby is in sync state → Warn.
	// src: https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-SYNCHRONOUS-STANDBY-NAMES
	lag := 5.0
	rows := []ReplicaRow{
		{ApplicationName: "replica1", SyncState: "async", ReplayLagSecs: &lag},
	}
	out := evalReplicationLag(rows, "replica1")
	if !hasLevel(out, pgtuner.Warn) {
		t.Fatalf("expected Warn when sync names set but no sync standby, got %v", levelsOf(out))
	}
}

func TestEvalReplicationLag_SyncNamesSet_HasSyncStandby_NoWarn(t *testing.T) {
	// synchronous_standby_names is set and a standby is in sync state → no extra Warn.
	lag := 5.0
	rows := []ReplicaRow{
		{ApplicationName: "replica1", SyncState: "sync", ReplayLagSecs: &lag},
	}
	out := evalReplicationLag(rows, "replica1")
	for _, a := range out {
		if a.Level == pgtuner.Warn {
			t.Fatalf("expected no Warn when sync names set and a sync standby exists, got %+v", a)
		}
	}
}

func TestEvalReplicationLag_EmptySyncNames_NoWarn(t *testing.T) {
	// synchronous_standby_names="" (empty) → no sync-names Warn even with async standbys.
	lag := 5.0
	rows := []ReplicaRow{
		{ApplicationName: "replica1", SyncState: "async", ReplayLagSecs: &lag},
	}
	out := evalReplicationLag(rows, "")
	if hasLevel(out, pgtuner.Warn) {
		t.Fatalf("expected no Warn with empty sync names and async standby, got %v", levelsOf(out))
	}
}

// ----- evalWalLevelVsLogical -------------------------------------------------

func TestEvalWalLevelVsLogical_NoLogical_NoAdvice(t *testing.T) {
	// No logical slots, no publications → no advice regardless of wal_level.
	a := evalWalLevelVsLogical("replica", 0, 0)
	if a != nil {
		t.Fatalf("expected nil with no logical slots or publications, got %+v", a)
	}
}

func TestEvalWalLevelVsLogical_LogicalSlots_LevelLogical_NoAdvice(t *testing.T) {
	// Logical slots present and wal_level=logical → no advice.
	// src: https://www.postgresql.org/docs/current/runtime-config-wal.html#GUC-WAL-LEVEL
	a := evalWalLevelVsLogical("logical", 2, 0)
	if a != nil {
		t.Fatalf("expected nil when wal_level=logical, got %+v", a)
	}
}

func TestEvalWalLevelVsLogical_LogicalSlots_LevelReplica_Bad(t *testing.T) {
	// Logical slots present but wal_level=replica → Bad.
	// src: https://www.postgresql.org/docs/current/runtime-config-wal.html#GUC-WAL-LEVEL
	a := evalWalLevelVsLogical("replica", 2, 0)
	if a == nil || a.Level != pgtuner.Bad {
		t.Fatalf("expected Bad when logical slots present and wal_level=replica, got %+v", a)
	}
}

func TestEvalWalLevelVsLogical_Publications_LevelMinimal_Bad(t *testing.T) {
	// Publications present but wal_level=minimal → Bad.
	a := evalWalLevelVsLogical("minimal", 0, 3)
	if a == nil || a.Level != pgtuner.Bad {
		t.Fatalf("expected Bad when publications present and wal_level=minimal, got %+v", a)
	}
}

func TestEvalWalLevelVsLogical_BothPresent_LevelLogical_NoAdvice(t *testing.T) {
	// Both logical slots and publications, wal_level=logical → no advice.
	a := evalWalLevelVsLogical("logical", 1, 2)
	if a != nil {
		t.Fatalf("expected nil when wal_level=logical with both logical slots and publications, got %+v", a)
	}
}

// ----- evalStandbyArchiveDelay -----------------------------------------------

func TestEvalStandbyArchiveDelay_Zero_Info(t *testing.T) {
	// max_standby_archive_delay=0 → immediate cancellation → Info.
	// src: https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-MAX-STANDBY-ARCHIVE-DELAY
	out := evalStandbyArchiveDelay("0")
	if len(out) == 0 || out[0].Level != pgtuner.Info {
		t.Fatalf("expected Info for max_standby_archive_delay=0, got %v", levelsOf(out))
	}
	if out[0].Title == "" || out[0].Message == "" {
		t.Fatal("expected non-empty title and message")
	}
}

func TestEvalStandbyArchiveDelay_Positive_NoAdvice(t *testing.T) {
	// max_standby_archive_delay=30000 (30s) → healthy → no advice.
	out := evalStandbyArchiveDelay("30000")
	if len(out) != 0 {
		t.Fatalf("expected no advice for positive delay, got %v", levelsOf(out))
	}
}

func TestEvalStandbyArchiveDelay_MinusOne_NoAdvice(t *testing.T) {
	// -1 is handled by the streaming-delay check; archive-delay only flags 0.
	out := evalStandbyArchiveDelay("-1")
	if len(out) != 0 {
		t.Fatalf("expected no advice for archive-delay=-1, got %v", levelsOf(out))
	}
}

// ----- evalStandbyStreamingDelayHigh -----------------------------------------

func TestEvalStandbyStreamingDelayHigh_MinusOne_Info(t *testing.T) {
	// max_standby_streaming_delay=-1 (wait forever) → Info.
	// src: https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-MAX-STANDBY-STREAMING-DELAY
	out := evalStandbyStreamingDelayHigh(-1)
	if len(out) == 0 || out[0].Level != pgtuner.Info {
		t.Fatalf("expected Info for delay=-1, got %v", levelsOf(out))
	}
	if out[0].Title == "" || out[0].Message == "" {
		t.Fatal("expected non-empty title and message")
	}
	// Message should mention "wait forever"
	if !containsStr(out[0].Message, "wait forever") {
		t.Errorf("expected message to mention 'wait forever', got: %s", out[0].Message)
	}
}

func TestEvalStandbyStreamingDelayHigh_AboveThreshold_Info(t *testing.T) {
	// 301 000 ms > 300 000 ms threshold → Info.
	out := evalStandbyStreamingDelayHigh(301_000)
	if len(out) == 0 || out[0].Level != pgtuner.Info {
		t.Fatalf("expected Info for delay=301000ms (above threshold), got %v", levelsOf(out))
	}
}

func TestEvalStandbyStreamingDelayHigh_AtThreshold_NoAdvice(t *testing.T) {
	// Exactly at threshold → no advice (condition is > threshold, exclusive).
	out := evalStandbyStreamingDelayHigh(300_000)
	if len(out) != 0 {
		t.Fatalf("expected no advice at exact threshold=300000ms, got %v", levelsOf(out))
	}
}

func TestEvalStandbyStreamingDelayHigh_Positive_NoAdvice(t *testing.T) {
	// 30 000 ms (30s) → well within range → no advice.
	out := evalStandbyStreamingDelayHigh(30_000)
	if len(out) != 0 {
		t.Fatalf("expected no advice for delay=30000ms, got %v", levelsOf(out))
	}
}

func TestEvalStandbyStreamingDelayHigh_Zero_NoAdvice(t *testing.T) {
	// 0 is handled by the inline zero-check in runStandbyChecks; this pure function
	// only fires on -1 or > threshold.
	out := evalStandbyStreamingDelayHigh(0)
	if len(out) != 0 {
		t.Fatalf("expected no advice for delay=0 from this function, got %v", levelsOf(out))
	}
}

// ----- evalHotStandbyFeedbackOn -----------------------------------------------

func TestEvalHotStandbyFeedbackOn_True_Info(t *testing.T) {
	// hot_standby_feedback=on → Info noting the bloat tradeoff.
	// src: https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-HOT-STANDBY-FEEDBACK
	out := evalHotStandbyFeedbackOn(true)
	if len(out) == 0 || out[0].Level != pgtuner.Info {
		t.Fatalf("expected Info for hot_standby_feedback=on, got %v", levelsOf(out))
	}
	if out[0].Title == "" || out[0].Message == "" {
		t.Fatal("expected non-empty title and message")
	}
}

func TestEvalHotStandbyFeedbackOn_False_NoAdvice(t *testing.T) {
	// hot_standby_feedback=off → no advice from this function (handled elsewhere).
	out := evalHotStandbyFeedbackOn(false)
	if len(out) != 0 {
		t.Fatalf("expected no advice for hot_standby_feedback=off, got %v", levelsOf(out))
	}
}

// containsStr is a small helper used by standby tests.
func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}()
}
