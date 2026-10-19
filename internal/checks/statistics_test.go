package checks

import (
	"testing"

	"github.com/soylic/pgtuner/internal/pg"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

func runStatistics(settings map[string]string) []pgtuner.Advice {
	return evalStatistics(pg.NewSettings(settings))
}

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

func TestStatisticsSharedPreloadLibrariesAbsentKey(t *testing.T) {
	got := runStatistics(map[string]string{
		"track_io_timing": "on",
		"track_counts":    "on",
	})
	for _, a := range got {
		if a.Title == "pg_stat_statements is not loaded" {
			t.Fatalf("absent shared_preload_libraries key should not produce not-loaded advice, got %v", a)
		}
	}
}

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
