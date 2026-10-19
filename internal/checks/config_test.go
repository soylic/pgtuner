package checks

import (
	"testing"
	"time"

	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// findLevel reports whether any advice in a has the given level.
func findLevel(a []pgtuner.Advice, want pgtuner.Level) bool {
	for _, x := range a {
		if x.Level == want {
			return true
		}
	}
	return false
}

func TestEvalPendingRestart(t *testing.T) {
	if got := evalPendingRestart(nil); got != nil {
		t.Errorf("empty list: want nil, got %+v", got)
	}
	if got := evalPendingRestart([]string{}); got != nil {
		t.Errorf("zero-len list: want nil, got %+v", got)
	}

	got := evalPendingRestart([]string{"shared_buffers"})
	if len(got) != 1 {
		t.Fatalf("non-empty: want 1 advice, got %d", len(got))
	}
	if got[0].Level != pgtuner.Warn {
		t.Errorf("non-empty: want Warn, got %s", got[0].Level)
	}
	if !containsStr(got[0].Message, "shared_buffers") {
		t.Errorf("expected setting name in message, got %q", got[0].Message)
	}
}

func TestEvalPendingRestartCap(t *testing.T) {
	names := make([]string, configListCap+5)
	for i := range names {
		names[i] = "setting"
	}
	got := evalPendingRestart(names)
	if len(got) != 1 {
		t.Fatalf("want 1 advice, got %d", len(got))
	}
	if !containsStr(got[0].Message, "and 5 more") {
		t.Errorf("expected capped summary, got %q", got[0].Message)
	}
}

func TestEvalParallelismPerGatherBoundary(t *testing.T) {
	// per_gather == 0 with >1 CPU → Info (parallel disabled).
	got := evalParallelism(8, 8, 0, 4)
	if !findLevel(got, pgtuner.Info) {
		t.Errorf("per_gather=0, cpus=4: want Info, got %+v", got)
	}
	// per_gather == 1 with >1 CPU → no "parallel disabled" advice.
	got = evalParallelism(8, 8, 1, 4)
	for _, a := range got {
		if a.Title == "Parallel query is disabled" {
			t.Errorf("per_gather=1 should not emit 'Parallel query is disabled': %+v", a)
		}
	}
	// per_gather == 0 with exactly 1 CPU → no advice (nothing to parallelise).
	got = evalParallelism(8, 8, 0, 1)
	for _, a := range got {
		if a.Title == "Parallel query is disabled" {
			t.Errorf("per_gather=0 cpus=1 should not emit 'Parallel query is disabled': %+v", a)
		}
	}
}

func TestEvalParallelismOversubscribed(t *testing.T) {
	// max_parallel_workers (16) > cpus (4) → Info.
	got := evalParallelism(16, 16, 2, 4)
	found := false
	for _, a := range got {
		if a.Title == "max_parallel_workers exceeds available CPUs" {
			found = true
		}
	}
	if !found {
		t.Errorf("workers=16 cpus=4: want oversubscribed Info, got %+v", got)
	}

	// workers == cpus → not oversubscribed (boundary: strictly greater).
	got = evalParallelism(8, 4, 2, 4)
	for _, a := range got {
		if a.Title == "max_parallel_workers exceeds available CPUs" {
			t.Errorf("workers=4 cpus=4 should not be oversubscribed: %+v", a)
		}
	}
}

func TestEvalParallelismCappedByPool(t *testing.T) {
	// max_parallel_workers (16) > max_worker_processes (8) → Info.
	got := evalParallelism(8, 16, 2, 8)
	found := false
	for _, a := range got {
		if a.Title == "max_parallel_workers exceeds max_worker_processes" {
			found = true
		}
	}
	if !found {
		t.Errorf("workers=16 pool=8: want capped Info, got %+v", got)
	}

	// workers == pool → not capped (boundary: strictly greater).
	got = evalParallelism(8, 8, 2, 8)
	for _, a := range got {
		if a.Title == "max_parallel_workers exceeds max_worker_processes" {
			t.Errorf("workers=8 pool=8 should not be capped: %+v", a)
		}
	}
}

func TestEvalParallelismNoCPUFacts(t *testing.T) {
	// cpus <= 0 (ErrNoOSFacts) → CPU-relative checks skipped; pool check still
	// runs.
	got := evalParallelism(8, 16, 0, -1)
	for _, a := range got {
		if a.Title == "Parallel query is disabled" {
			t.Errorf("no CPU facts must skip per-gather check: %+v", a)
		}
		if a.Title == "max_parallel_workers exceeds available CPUs" {
			t.Errorf("no CPU facts must skip oversubscribe check: %+v", a)
		}
	}
	found := false
	for _, a := range got {
		if a.Title == "max_parallel_workers exceeds max_worker_processes" {
			found = true
		}
	}
	if !found {
		t.Errorf("pool check should still run without CPU facts, got %+v", got)
	}
}

func TestEvalParallelismHealthy(t *testing.T) {
	// per_gather=2, workers=4 ≤ cpus=8, workers ≤ pool=8 → no advice.
	if got := evalParallelism(8, 4, 2, 8); got != nil {
		t.Errorf("healthy config: want nil, got %+v", got)
	}
}

func TestEvalLoggingHygieneAllDisabled(t *testing.T) {
	h := logHygiene{
		logMinDurationStatement: -1,
		logLockWaits:            false,
		logAutovacuumMinDur:     -1,
		logCheckpoints:          false,
		logCheckpointsPresent:   true,
		checkpointsDefaultOn:    true,
	}
	got := evalLoggingHygiene(h)
	if len(got) != 4 {
		t.Fatalf("all disabled: want 4 advices, got %d: %+v", len(got), got)
	}
	for _, a := range got {
		if a.Level != pgtuner.Info {
			t.Errorf("want Info, got %s for %q", a.Level, a.Title)
		}
	}
}

func TestEvalLoggingHygieneAllEnabled(t *testing.T) {
	h := logHygiene{
		logMinDurationStatement: 1000,
		logLockWaits:            true,
		logAutovacuumMinDur:     0,
		logCheckpoints:          true,
		logCheckpointsPresent:   true,
		checkpointsDefaultOn:    true,
	}
	if got := evalLoggingHygiene(h); got != nil {
		t.Errorf("all enabled: want nil, got %+v", got)
	}
}

func TestEvalLoggingHygieneBoundaries(t *testing.T) {
	// log_min_duration_statement = 0 (log everything) → not disabled.
	h := logHygiene{logMinDurationStatement: 0, logLockWaits: true, logAutovacuumMinDur: 0, logCheckpoints: true, logCheckpointsPresent: true}
	for _, a := range evalLoggingHygiene(h) {
		if a.Title == "Slow-query logging is disabled" {
			t.Errorf("lmds=0 should not be disabled: %+v", a)
		}
	}
	// log_autovacuum_min_duration = 0 → not disabled.
	h = logHygiene{logMinDurationStatement: 1000, logLockWaits: true, logAutovacuumMinDur: 0, logCheckpoints: true, logCheckpointsPresent: true}
	for _, a := range evalLoggingHygiene(h) {
		if a.Title == "Autovacuum logging is disabled" {
			t.Errorf("lavd=0 should not be disabled: %+v", a)
		}
	}
}

func TestEvalLoggingHygieneCheckpointsAbsent(t *testing.T) {
	// log_checkpoints not present in pg_settings → no checkpoint advice.
	h := logHygiene{logMinDurationStatement: 1000, logLockWaits: true, logAutovacuumMinDur: 0, logCheckpoints: false, logCheckpointsPresent: false}
	for _, a := range evalLoggingHygiene(h) {
		if a.Title == "log_checkpoints is disabled" {
			t.Errorf("absent log_checkpoints should emit nothing: %+v", a)
		}
	}
}

func TestEvalTimeoutsAllZero(t *testing.T) {
	// PG<17: three timeout Infos, no transaction_timeout.
	got := evalTimeouts(timeoutSettings{statementTimeout: 0, idleInTxnTimeout: 0, lockTimeout: 0})
	if len(got) != 3 {
		t.Fatalf("PG<17 all-zero: want 3 advices, got %d: %+v", len(got), got)
	}
}

func TestEvalTimeoutsAllZeroPG17(t *testing.T) {
	// PG≥17: four timeout Infos including transaction_timeout.
	got := evalTimeouts(timeoutSettings{
		statementTimeout: 0, idleInTxnTimeout: 0, lockTimeout: 0,
		transactionTimeout: 0, transactionTimeoutSupp: true,
	})
	if len(got) != 4 {
		t.Fatalf("PG17 all-zero: want 4 advices, got %d: %+v", len(got), got)
	}
	found := false
	for _, a := range got {
		if containsStr(a.Title, "transaction_timeout") {
			found = true
		}
	}
	if !found {
		t.Errorf("PG17 all-zero: want transaction_timeout advice, got %+v", got)
	}
}

func TestEvalTimeoutsAllSet(t *testing.T) {
	// Every timeout set to a positive value → no advice.
	got := evalTimeouts(timeoutSettings{
		statementTimeout: 30000, idleInTxnTimeout: 300000, lockTimeout: 5000,
		transactionTimeout: 60000, transactionTimeoutSupp: true,
	})
	if got != nil {
		t.Errorf("all set: want nil, got %+v", got)
	}
}

func TestEvalTimeoutsTransactionGate(t *testing.T) {
	// transactionTimeout=0 but supp=false (PG<17) → no transaction_timeout advice.
	got := evalTimeouts(timeoutSettings{
		statementTimeout: 30000, idleInTxnTimeout: 300000, lockTimeout: 5000,
		transactionTimeout: 0, transactionTimeoutSupp: false,
	})
	if got != nil {
		t.Errorf("PG<17 with timeouts set: want nil (no transaction_timeout), got %+v", got)
	}
}

func TestEvalArchiverHealthy(t *testing.T) {
	// No failures, recent success → nil.
	now := time.Now()
	got := evalArchiver(archiverStatus{
		failedCount:       0,
		lastArchivedTime:  now,
		lastArchivedValid: true,
	})
	if got != nil {
		t.Errorf("healthy archiver: want nil, got %+v", got)
	}
}

func TestEvalArchiverFailedCount(t *testing.T) {
	// failed_count > 0 → Warn even if the last failure is older than the last
	// success.
	now := time.Now()
	got := evalArchiver(archiverStatus{
		failedCount:       3,
		lastFailedTime:    now.Add(-time.Hour),
		lastFailedValid:   true,
		lastArchivedTime:  now,
		lastArchivedValid: true,
	})
	if len(got) != 1 || got[0].Level != pgtuner.Warn {
		t.Errorf("failed_count=3: want one Warn, got %+v", got)
	}
}

func TestEvalArchiverFailingNow(t *testing.T) {
	// failed_count counter could be 0 after a stats reset, but last_failed_time is
	// newer than last_archived_time → still failing → Warn.
	now := time.Now()
	got := evalArchiver(archiverStatus{
		failedCount:       0,
		lastFailedTime:    now,
		lastFailedValid:   true,
		lastArchivedTime:  now.Add(-time.Hour),
		lastArchivedValid: true,
	})
	if len(got) != 1 || got[0].Level != pgtuner.Warn {
		t.Errorf("last failure newer than last success: want Warn, got %+v", got)
	}
}

func TestEvalArchiverFailedNoSuccess(t *testing.T) {
	// A failure exists and there has been no successful archive at all → Warn.
	now := time.Now()
	got := evalArchiver(archiverStatus{
		failedCount:     0,
		lastFailedTime:  now,
		lastFailedValid: true,
	})
	if len(got) != 1 || got[0].Level != pgtuner.Warn {
		t.Errorf("failure with no success: want Warn, got %+v", got)
	}
}

func TestEvalArchiverNeverRan(t *testing.T) {
	// Nothing archived, nothing failed (fresh archiver) → nil.
	if got := evalArchiver(archiverStatus{}); got != nil {
		t.Errorf("fresh archiver: want nil, got %+v", got)
	}
}

func TestEvalOutdatedExtensions(t *testing.T) {
	if got := evalOutdatedExtensions(nil); got != nil {
		t.Errorf("empty: want nil, got %+v", got)
	}
	got := evalOutdatedExtensions([]string{"pg_stat_statements (1.9→1.10)"})
	if len(got) != 1 || got[0].Level != pgtuner.Info {
		t.Fatalf("non-empty: want one Info, got %+v", got)
	}
	if !containsStr(got[0].Message, "1.9→1.10") {
		t.Errorf("expected version detail in message, got %q", got[0].Message)
	}
}
