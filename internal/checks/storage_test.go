package checks

import (
	"testing"

	"github.com/OpenRemedy/pgtuner/internal/pg"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// --- evalTempFiles unit tests -------------------------------------------------

// TestEvalTempFilesZero: no temp files → no advice (empty slice).
func TestEvalTempFilesZero(t *testing.T) {
	got := evalTempFiles(0, 0)
	if len(got) != 0 {
		t.Fatalf("expected no advice for zero temp files, got %v", got)
	}
}

// TestEvalTempFilesSmallBytes: nonzero temp_files but tiny bytes (< 1 GiB) → no Warn.
func TestEvalTempFilesSmallBytes(t *testing.T) {
	// 5 files, 100 MiB — below the 1 GiB threshold.
	got := evalTempFiles(5, 100*1024*1024)
	for _, a := range got {
		if a.Level == pgtuner.Warn {
			t.Fatalf("expected no Warn for small temp_bytes, got %v", got)
		}
	}
}

// TestEvalTempFilesLarge: nonzero temp_files AND bytes > 1 GiB → Warn.
func TestEvalTempFilesLarge(t *testing.T) {
	// 10 files, 2 GiB — above threshold.
	got := evalTempFiles(10, 2*1024*1024*1024)
	if len(got) == 0 || got[0].Level != pgtuner.Warn {
		t.Fatalf("expected Warn for large temp spill, got %v", got)
	}
}

// TestEvalTempFilesExactThreshold: bytes == 1 GiB exactly → no Warn (> not >=).
func TestEvalTempFilesExactThreshold(t *testing.T) {
	got := evalTempFiles(1, 1*1024*1024*1024)
	for _, a := range got {
		if a.Level == pgtuner.Warn {
			t.Fatalf("exactly 1 GiB should NOT trigger Warn (threshold is > 1 GiB), got %v", got)
		}
	}
}

// --- evalStorageSettings unit tests -------------------------------------------

// helper: run settings-only storage evaluation.
func runStorageSettings(settings map[string]string, major, minor int) []pgtuner.Advice {
	return evalStorageSettings(
		pg.NewSettings(settings),
		pg.ServerVersion{Major: major, Minor: minor},
	)
}

// TestStorageDataChecksumsOffPrePG18: data_checksums=off on PG16 → Info (not Warn).
func TestStorageDataChecksumsOffPrePG18(t *testing.T) {
	got := runStorageSettings(map[string]string{
		"data_checksums": "off",
	}, 16, 0)
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for data_checksums=off on PG16, got %v", levelsOf(got))
	}
	for _, a := range got {
		if a.Title == "Data checksums are disabled" && a.Level == pgtuner.Warn {
			t.Fatalf("PG16 data_checksums=off should be Info not Warn, got %v", a)
		}
	}
}

// TestStorageDataChecksumsOffPG18: data_checksums=off on PG18 → Warn.
func TestStorageDataChecksumsOffPG18(t *testing.T) {
	got := runStorageSettings(map[string]string{
		"data_checksums": "off",
	}, 18, 0)
	if !hasLevel(got, pgtuner.Warn) {
		t.Fatalf("expected Warn for data_checksums=off on PG18, got %v", levelsOf(got))
	}
}

// TestStorageDataChecksumsOn: data_checksums=on → no checksums advice.
func TestStorageDataChecksumsOn(t *testing.T) {
	got := runStorageSettings(map[string]string{
		"data_checksums": "on",
	}, 16, 0)
	for _, a := range got {
		if a.Title == "Data checksums are disabled" {
			t.Fatalf("data_checksums=on should produce no checksums advice, got %v", a)
		}
	}
}

// TestStorageTOASTCompressionPGLZ: default_toast_compression=pglz on PG14 → Info.
func TestStorageTOASTCompressionPGLZ(t *testing.T) {
	got := runStorageSettings(map[string]string{
		"default_toast_compression": "pglz",
	}, 14, 0)
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for default_toast_compression=pglz on PG14, got %v", levelsOf(got))
	}
}

// TestStorageTOASTCompressionLZ4: default_toast_compression=lz4 → no advice.
func TestStorageTOASTCompressionLZ4(t *testing.T) {
	got := runStorageSettings(map[string]string{
		"default_toast_compression": "lz4",
	}, 16, 0)
	for _, a := range got {
		if a.Title == "Consider lz4 for TOAST compression" {
			t.Fatalf("lz4 toast compression should not trigger advice, got %v", a)
		}
	}
}

// TestStorageTOASTCompressionSkippedPrePG14: default_toast_compression check
// should not fire on PG13 (GUC did not exist).
func TestStorageTOASTCompressionSkippedPrePG14(t *testing.T) {
	got := runStorageSettings(map[string]string{
		"default_toast_compression": "pglz", // present in map but PG version is 13
	}, 13, 0)
	for _, a := range got {
		if a.Title == "Consider lz4 for TOAST compression" {
			t.Fatalf("TOAST advice should be skipped on PG13, got %v", a)
		}
	}
}

// TestStorageLogTempFilesDisabled: log_temp_files=-1 → Info.
func TestStorageLogTempFilesDisabled(t *testing.T) {
	got := runStorageSettings(map[string]string{
		"log_temp_files": "-1",
	}, 16, 0)
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for log_temp_files=-1, got %v", levelsOf(got))
	}
}

// TestStorageLogTempFilesEnabled: log_temp_files=0 → no log_temp_files advice.
func TestStorageLogTempFilesEnabled(t *testing.T) {
	got := runStorageSettings(map[string]string{
		"log_temp_files": "0",
	}, 16, 0)
	for _, a := range got {
		if a.Title == "log_temp_files is disabled" {
			t.Fatalf("log_temp_files=0 should not trigger disabled advice, got %v", a)
		}
	}
}
