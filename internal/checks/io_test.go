package checks

import (
	"context"
	"testing"

	"github.com/OpenRemedy/pgtuner/internal/osinspect"
	"github.com/OpenRemedy/pgtuner/internal/pg"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// --- evalIO unit tests --------------------------------------------------------

// TestEvalIOIdle: all-zero counters → no advice (OK path handled by Run).
func TestEvalIOIdle(t *testing.T) {
	rows := []IORow{
		{BackendType: "client backend", Context: "normal", Evictions: 0, Fsyncs: 0, Writes: 0},
		{BackendType: "client backend", Context: "bulkread", Evictions: 0, Fsyncs: 0, Writes: 0},
	}
	got := evalIO(rows)
	for _, a := range got {
		if a.Level == pgtuner.Warn || a.Level == pgtuner.Bad {
			t.Fatalf("idle system should not produce Warn/Bad, got %v", got)
		}
	}
}

// TestEvalIOClientEvictionHighWarn: evictions ≥ threshold → Warn.
func TestEvalIOClientEvictionHighWarn(t *testing.T) {
	rows := []IORow{
		{BackendType: "client backend", Context: "normal", Evictions: ioClientEvictionWarnThreshold, Fsyncs: 0, Writes: 0},
	}
	got := evalIO(rows)
	if !hasLevel(got, pgtuner.Warn) {
		t.Fatalf("expected Warn for client backend evictions=%d, got %v", ioClientEvictionWarnThreshold, levelsOf(got))
	}
}

// TestEvalIOClientEvictionBelowThreshold: evictions < threshold → no Warn.
func TestEvalIOClientEvictionBelowThreshold(t *testing.T) {
	rows := []IORow{
		{BackendType: "client backend", Context: "normal", Evictions: ioClientEvictionWarnThreshold - 1, Fsyncs: 0, Writes: 0},
	}
	got := evalIO(rows)
	for _, a := range got {
		if a.Level == pgtuner.Warn {
			t.Fatalf("evictions below threshold should not trigger Warn, got %v", got)
		}
	}
}

// TestEvalIOClientFsyncHighWarn: fsyncs ≥ threshold → Warn.
func TestEvalIOClientFsyncHighWarn(t *testing.T) {
	rows := []IORow{
		{BackendType: "client backend", Context: "normal", Evictions: 0, Fsyncs: ioClientFsyncWarnThreshold, Writes: 0},
	}
	got := evalIO(rows)
	if !hasLevel(got, pgtuner.Warn) {
		t.Fatalf("expected Warn for client backend fsyncs=%d, got %v", ioClientFsyncWarnThreshold, levelsOf(got))
	}
}

// TestEvalIOClientFsyncBelowThreshold: fsyncs < threshold → no Warn.
func TestEvalIOClientFsyncBelowThreshold(t *testing.T) {
	rows := []IORow{
		{BackendType: "client backend", Context: "normal", Evictions: 0, Fsyncs: ioClientFsyncWarnThreshold - 1, Writes: 0},
	}
	got := evalIO(rows)
	for _, a := range got {
		if a.Level == pgtuner.Warn {
			t.Fatalf("fsyncs below threshold should not trigger Warn, got %v", got)
		}
	}
}

// TestEvalIOBulkreadEvictionsIgnored: evictions in bulkread context should NOT
// trigger a Warn; bulk-read operations use a dedicated buffer ring and are
// expected to evict.
func TestEvalIOBulkreadEvictionsIgnored(t *testing.T) {
	rows := []IORow{
		{BackendType: "client backend", Context: "bulkread", Evictions: ioClientEvictionWarnThreshold * 10, Fsyncs: 0, Writes: 0},
	}
	got := evalIO(rows)
	for _, a := range got {
		if a.Level == pgtuner.Warn {
			t.Fatalf("bulkread evictions should not trigger Warn (different buffer ring), got %v", got)
		}
	}
}

// TestEvalIONonClientBackendIgnored: evictions from bgwriter/checkpointer are
// expected and must not trigger a Warn.
func TestEvalIONonClientBackendIgnored(t *testing.T) {
	rows := []IORow{
		{BackendType: "background writer", Context: "normal", Evictions: 100000, Fsyncs: 5000, Writes: 0},
		{BackendType: "checkpointer", Context: "normal", Evictions: 50000, Fsyncs: 10000, Writes: 0},
	}
	got := evalIO(rows)
	for _, a := range got {
		if a.Level == pgtuner.Warn {
			t.Fatalf("bgwriter/checkpointer evictions/fsyncs should not trigger Warn, got %v", got)
		}
	}
}

// TestEvalIOEmptyRows: no rows → no advice (empty system).
func TestEvalIOEmptyRows(t *testing.T) {
	got := evalIO(nil)
	if len(got) != 0 {
		t.Fatalf("expected no advice for empty rows, got %v", got)
	}
}

// --- PG<16 path unit test -----------------------------------------------------

// TestIOCheckPrePG16NoQuery: Version 15, Conn nil → exactly one Info, no panic.
// The PG<16 path must return early without touching Conn.
func TestIOCheckPrePG16NoQuery(t *testing.T) {
	probe := &Probe{
		Version:  pg.ServerVersion{Major: 15, Minor: 0},
		Conn:     nil, // must NOT be touched on the <16 path
		Settings: pg.NewSettings(map[string]string{}),
		OS:       osinspect.NoopInspector{},
	}
	got := IOCheck{}.Run(context.Background(), probe)
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 advice for PG15, got %d: %v", len(got), got)
	}
	if got[0].Level != pgtuner.Info {
		t.Fatalf("expected Info for PG<16 path, got %v", got[0].Level)
	}
}

// --- evalIO writes unit test ---------------------------------------------------

// TestEvalIOClientWriteHighWarn: writes ≥ threshold in normal context → Warn.
func TestEvalIOClientWriteHighWarn(t *testing.T) {
	rows := []IORow{
		{BackendType: "client backend", Context: "normal", Evictions: 0, Fsyncs: 0, Writes: ioClientWriteWarnThreshold},
	}
	got := evalIO(rows)
	if !hasLevel(got, pgtuner.Warn) {
		t.Fatalf("expected Warn for client backend writes=%d, got %v", ioClientWriteWarnThreshold, levelsOf(got))
	}
}

// TestEvalIOClientWriteBelowThreshold: writes < threshold → no Warn.
func TestEvalIOClientWriteBelowThreshold(t *testing.T) {
	rows := []IORow{
		{BackendType: "client backend", Context: "normal", Evictions: 0, Fsyncs: 0, Writes: ioClientWriteWarnThreshold - 1},
	}
	got := evalIO(rows)
	for _, a := range got {
		if a.Level == pgtuner.Warn {
			t.Fatalf("writes below threshold should not trigger Warn, got %v", got)
		}
	}
}

// TestEvalIOBulkreadWritesIgnored: writes in bulkread context should NOT trigger
// a Warn; bulk-read operations use a dedicated buffer ring.
func TestEvalIOBulkreadWritesIgnored(t *testing.T) {
	rows := []IORow{
		{BackendType: "client backend", Context: "bulkread", Evictions: 0, Fsyncs: 0, Writes: ioClientWriteWarnThreshold * 10},
	}
	got := evalIO(rows)
	for _, a := range got {
		if a.Level == pgtuner.Warn {
			t.Fatalf("bulkread writes should not trigger Warn, got %v", got)
		}
	}
}

// --- evalIOMethodPG18 unit tests -----------------------------------------------

// TestEvalIOMethodPG18DefaultWorker: io_method=worker (the PG18 default) → no advice.
// A stock PG18 server boots with io_method=worker and must not generate spurious Info.
func TestEvalIOMethodPG18DefaultWorker(t *testing.T) {
	s := pg.NewSettings(map[string]string{"io_method": "worker", "io_workers": "3"})
	got := evalIOMethodPG18(s)
	if len(got) != 0 {
		t.Fatalf("default io_method=worker should produce no advice, got %v", got)
	}
}

// TestEvalIOMethodPG18SyncDisablesAsync: io_method=sync disables async I/O → Info.
// 'sync' is NOT the PG18 default; operators who set it should see an informational note.
func TestEvalIOMethodPG18SyncDisablesAsync(t *testing.T) {
	s := pg.NewSettings(map[string]string{"io_method": "sync", "io_workers": "3"})
	got := evalIOMethodPG18(s)
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for io_method=sync (async I/O disabled), got %v", levelsOf(got))
	}
}

// TestEvalIOMethodPG18IOUring: io_method=io_uring → Info (kernel dependency noted).
func TestEvalIOMethodPG18IOUring(t *testing.T) {
	s := pg.NewSettings(map[string]string{"io_method": "io_uring", "io_workers": "3"})
	got := evalIOMethodPG18(s)
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for io_method=io_uring, got %v", levelsOf(got))
	}
}

// TestEvalIOMethodPG18WorkersNonDefaultWithWorkerMethod: io_workers != 3 when
// io_method=worker → Info (non-default worker count with async I/O active).
func TestEvalIOMethodPG18WorkersNonDefaultWithWorkerMethod(t *testing.T) {
	s := pg.NewSettings(map[string]string{"io_method": "worker", "io_workers": "8"})
	got := evalIOMethodPG18(s)
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for io_workers=8 with io_method=worker, got %v", levelsOf(got))
	}
}

// TestEvalIOMethodPG18WorkersNonDefaultNoMethod: io_workers != 3 with no explicit
// io_method (implies default 'worker' is active) → Info.
func TestEvalIOMethodPG18WorkersNonDefaultNoMethod(t *testing.T) {
	s := pg.NewSettings(map[string]string{"io_workers": "8"})
	got := evalIOMethodPG18(s)
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for io_workers=8 (no io_method key present), got %v", levelsOf(got))
	}
}

// TestEvalIOMethodPG18WorkersIgnoredWhenSync: io_workers != 3 but io_method=sync
// means worker processes are unused → no Info for io_workers.
func TestEvalIOMethodPG18WorkersIgnoredWhenSync(t *testing.T) {
	s := pg.NewSettings(map[string]string{"io_method": "sync", "io_workers": "8"})
	got := evalIOMethodPG18(s)
	// We expect exactly the sync Info, not a workers Info.
	for _, a := range got {
		if a.Title == "io_workers is set to a non-default value (PG18+)" {
			t.Fatalf("should not emit io_workers advice when io_method=sync, got %v", got)
		}
	}
	// But we must still have the sync Info.
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for io_method=sync, got %v", levelsOf(got))
	}
}

// TestEvalIOMethodPG18Absent: settings absent → no advice (PG<18 won't have these GUCs).
func TestEvalIOMethodPG18Absent(t *testing.T) {
	s := pg.NewSettings(map[string]string{})
	got := evalIOMethodPG18(s)
	if len(got) != 0 {
		t.Fatalf("absent io_method/io_workers should produce no advice, got %v", got)
	}
}
