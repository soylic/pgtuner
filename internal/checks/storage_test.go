package checks

import (
	"testing"

	"github.com/soylic/pgtuner/internal/pg"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

func TestEvalTempFilesZero(t *testing.T) {
	got := evalTempFiles(0, 0)
	if len(got) != 0 {
		t.Fatalf("expected no advice for zero temp files, got %v", got)
	}
}

func TestEvalTempFilesSmallBytes(t *testing.T) {
	got := evalTempFiles(5, 100*1024*1024)
	for _, a := range got {
		if a.Level == pgtuner.Warn {
			t.Fatalf("expected no Warn for small temp_bytes, got %v", got)
		}
	}
}

func TestEvalTempFilesLarge(t *testing.T) {
	got := evalTempFiles(10, 2*1024*1024*1024)
	if len(got) == 0 || got[0].Level != pgtuner.Warn {
		t.Fatalf("expected Warn for large temp spill, got %v", got)
	}
}

func TestEvalTempFilesExactThreshold(t *testing.T) {
	got := evalTempFiles(1, 1*1024*1024*1024)
	for _, a := range got {
		if a.Level == pgtuner.Warn {
			t.Fatalf("exactly 1 GiB should NOT trigger Warn (threshold is > 1 GiB), got %v", got)
		}
	}
}

func runStorageSettings(settings map[string]string, major, minor int) []pgtuner.Advice {
	return evalStorageSettings(
		pg.NewSettings(settings),
		pg.ServerVersion{Major: major, Minor: minor},
	)
}

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

func TestStorageDataChecksumsOffPG18(t *testing.T) {
	got := runStorageSettings(map[string]string{
		"data_checksums": "off",
	}, 18, 0)
	if !hasLevel(got, pgtuner.Warn) {
		t.Fatalf("expected Warn for data_checksums=off on PG18, got %v", levelsOf(got))
	}
}

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

func TestStorageTOASTCompressionPGLZ(t *testing.T) {
	got := runStorageSettings(map[string]string{
		"default_toast_compression": "pglz",
	}, 14, 0)
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for default_toast_compression=pglz on PG14, got %v", levelsOf(got))
	}
}

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

func TestStorageTOASTCompressionSkippedPrePG14(t *testing.T) {
	got := runStorageSettings(map[string]string{
		"default_toast_compression": "pglz", // key present, but PG13 has no such GUC
	}, 13, 0)
	for _, a := range got {
		if a.Title == "Consider lz4 for TOAST compression" {
			t.Fatalf("TOAST advice should be skipped on PG13, got %v", a)
		}
	}
}

func TestStorageLogTempFilesDisabled(t *testing.T) {
	got := runStorageSettings(map[string]string{
		"log_temp_files": "-1",
	}, 16, 0)
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for log_temp_files=-1, got %v", levelsOf(got))
	}
}

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
