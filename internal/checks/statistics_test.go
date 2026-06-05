package checks

import (
	"testing"

	"github.com/OpenRemedy/pgtuner/internal/pg"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// helper: run statistics evaluation with a given settings map.
func runStatistics(settings map[string]string) []pgtuner.Advice {
	return evalStatistics(pg.NewSettings(settings))
}

// --- track_io_timing ----------------------------------------------------------

// TestStatisticsTrackIOTimingOff: track_io_timing=off → Info.
func TestStatisticsTrackIOTimingOff(t *testing.T) {
	got := runStatistics(map[string]string{
		"track_io_timing":          "off",
		"track_counts":             "on",
		"shared_preload_libraries": "pg_stat_statements",
	})
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for track_io_timing=off, got %v", levelsOf(got))
	}
	for _, a := range got {
		if a.Title == "track_io_timing is disabled" && a.Level != pgtuner.Info {
			t.Fatalf("track_io_timing advice should be Info, got %v", a.Level)
		}
	}
}

// TestStatisticsTrackIOTimingOn: track_io_timing=on → no track_io_timing advice.
func TestStatisticsTrackIOTimingOn(t *testing.T) {
	got := runStatistics(map[string]string{
		"track_io_timing":          "on",
		"track_counts":             "on",
		"shared_preload_libraries": "pg_stat_statements",
	})
	for _, a := range got {
		if a.Title == "track_io_timing is disabled" {
			t.Fatalf("track_io_timing=on should not produce disabled advice, got %v", a)
		}
	}
}

// --- track_counts -------------------------------------------------------------

// TestStatisticsTrackCountsOff: track_counts=off → Bad.
func TestStatisticsTrackCountsOff(t *testing.T) {
	got := runStatistics(map[string]string{
		"track_io_timing":          "on",
		"track_counts":             "off",
		"shared_preload_libraries": "pg_stat_statements",
	})
	if !hasLevel(got, pgtuner.Bad) {
		t.Fatalf("expected Bad for track_counts=off, got %v", levelsOf(got))
	}
}

// TestStatisticsTrackCountsOn: track_counts=on → no track_counts advice.
func TestStatisticsTrackCountsOn(t *testing.T) {
	got := runStatistics(map[string]string{
		"track_io_timing":          "on",
		"track_counts":             "on",
		"shared_preload_libraries": "pg_stat_statements",
	})
	for _, a := range got {
		if a.Title == "track_counts is disabled" {
			t.Fatalf("track_counts=on should not produce disabled advice, got %v", a)
		}
	}
}

// --- pg_stat_statements -------------------------------------------------------

// TestStatisticsPGStatStatementsAbsent: pg_stat_statements NOT in
// shared_preload_libraries → Info.
func TestStatisticsPGStatStatementsAbsent(t *testing.T) {
	got := runStatistics(map[string]string{
		"track_io_timing":          "on",
		"track_counts":             "on",
		"shared_preload_libraries": "",
	})
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for missing pg_stat_statements, got %v", levelsOf(got))
	}
	found := false
	for _, a := range got {
		if a.Title == "pg_stat_statements is not loaded" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected pg_stat_statements advice, got %v", got)
	}
}

// TestStatisticsPGStatStatementsPresent: pg_stat_statements in
// shared_preload_libraries → no pg_stat_statements advice.
func TestStatisticsPGStatStatementsPresent(t *testing.T) {
	got := runStatistics(map[string]string{
		"track_io_timing":          "on",
		"track_counts":             "on",
		"shared_preload_libraries": "pg_stat_statements",
	})
	for _, a := range got {
		if a.Title == "pg_stat_statements is not loaded" {
			t.Fatalf("pg_stat_statements present should not produce not-loaded advice, got %v", a)
		}
	}
}

// TestStatisticsPGStatStatementsAmongOthers: pg_stat_statements alongside other
// extensions in shared_preload_libraries → no advice.
func TestStatisticsPGStatStatementsAmongOthers(t *testing.T) {
	got := runStatistics(map[string]string{
		"track_io_timing":          "on",
		"track_counts":             "on",
		"shared_preload_libraries": "pg_stat_bgwriter,pg_stat_statements,auto_explain",
	})
	for _, a := range got {
		if a.Title == "pg_stat_statements is not loaded" {
			t.Fatalf("pg_stat_statements in multi-extension list should not produce not-loaded advice, got %v", a)
		}
	}
}

// TestStatisticsSharedPreloadLibrariesAbsentKey: shared_preload_libraries key
// missing entirely from settings → no pg_stat_statements advice (can't check
// what we can't read).
func TestStatisticsSharedPreloadLibrariesAbsentKey(t *testing.T) {
	got := runStatistics(map[string]string{
		"track_io_timing": "on",
		"track_counts":    "on",
		// shared_preload_libraries key is absent
	})
	for _, a := range got {
		if a.Title == "pg_stat_statements is not loaded" {
			t.Fatalf("absent shared_preload_libraries key should not produce not-loaded advice, got %v", a)
		}
	}
}

// --- OK fallthrough -----------------------------------------------------------

// TestStatisticsAllHealthy: all settings correct → OK.
func TestStatisticsAllHealthy(t *testing.T) {
	got := runStatistics(map[string]string{
		"track_io_timing":          "on",
		"track_counts":             "on",
		"shared_preload_libraries": "pg_stat_statements",
	})
	if !hasLevel(got, pgtuner.OK) {
		t.Fatalf("expected OK when all statistics settings are healthy, got %v", levelsOf(got))
	}
}

// TestStatisticsMultipleFire: track_io_timing=off AND track_counts=off → both
// Info and Bad fire together.
func TestStatisticsMultipleFire(t *testing.T) {
	got := runStatistics(map[string]string{
		"track_io_timing":          "off",
		"track_counts":             "off",
		"shared_preload_libraries": "pg_stat_statements",
	})
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for track_io_timing=off, got %v", levelsOf(got))
	}
	if !hasLevel(got, pgtuner.Bad) {
		t.Fatalf("expected Bad for track_counts=off, got %v", levelsOf(got))
	}
}
