package pgtuner

import "testing"

func rep(a ...Advice) *Report { return NewReport("17", a) }

func TestCompare(t *testing.T) {
	old := rep(
		Advice{ID: "wal.fsync-off", Level: Bad, Title: "fsync off"},
		Advice{ID: "connection.ssl-disabled", Level: Warn, Title: "SSL disabled"},
		Advice{ID: "cache.hit-ratio-low", Level: Warn, Title: "Cache low", Database: "app"},
		Advice{ID: "memory.shared-buffers-low", Level: Info, Title: "sb"},
		Advice{ID: "replication.slot-inactive", Level: Info, Title: "slot a"},
		Advice{ID: "replication.slot-inactive", Level: Warn, Title: "slot b"},
	)
	cur := rep(
		Advice{ID: "connection.ssl-disabled", Level: Warn, Title: "SSL disabled"},
		Advice{ID: "cache.hit-ratio-low", Level: Info, Title: "Cache low", Database: "app"},
		Advice{ID: "memory.shared-buffers-low", Level: Warn, Title: "sb"},
		Advice{ID: "replication.slot-inactive", Level: Warn, Title: "slot c"},
		Advice{ID: "io.sync-method", Level: Todo, Title: "io"},
	)
	d := Compare(old, cur)
	if len(d.Added) != 1 || d.Added[0].ID != "io.sync-method" {
		t.Errorf("Added = %+v", d.Added)
	}
	if len(d.Resolved) != 1 || d.Resolved[0].ID != "wal.fsync-off" {
		t.Errorf("Resolved = %+v", d.Resolved)
	}
	if len(d.Worse) != 1 || d.Worse[0].ID != "memory.shared-buffers-low" || *d.Worse[0].Old != Info || *d.Worse[0].New != Warn {
		t.Errorf("Worse = %+v", d.Worse)
	}
	if len(d.Better) != 1 || d.Better[0].Database != "app" {
		t.Errorf("Better = %+v", d.Better)
	}
	if !d.Regressed(Warn) {
		t.Error("a finding that turned Warn must count as a regression at warn")
	}
	if Compare(old, old).Regressed(Info) {
		t.Error("identical reports must not regress")
	}
}

func TestCompareUnknownIsNotARegression(t *testing.T) {
	old := rep(Advice{ID: "x.y", Level: Info, Title: "t"})
	cur := rep(Advice{ID: "x.y", Level: Unknown, Title: "t"})
	d := Compare(old, cur)
	if len(d.Worse) != 0 || d.Regressed(Info) {
		t.Errorf("Info -> Unknown must not be worse: %+v", d)
	}
}
