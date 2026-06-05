package checks

import (
	"context"
	"strings"
	"testing"

	"github.com/OpenRemedy/pgtuner/internal/osinspect"
	"github.com/OpenRemedy/pgtuner/internal/pg"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// helper: run WAL check with given settings and PG version.
func runWAL(settings map[string]string, major, minor int) []pgtuner.Advice {
	p := &Probe{
		Settings: pg.NewSettings(settings),
		OS:       osinspect.NoopInspector{},
		Version:  pg.ServerVersion{Major: major, Minor: minor},
	}
	return WALCheck{}.Run(context.Background(), p)
}

// healthyWALSettings returns a map of all-good WAL settings.
func healthyWALSettings() map[string]string {
	return map[string]string{
		"fsync":                "on",
		"full_page_writes":     "on",
		"synchronous_commit":   "on",
		"wal_compression":      "lz4",
		"wal_level":            "replica",
	}
}

// TestWALFsyncOff: fsync=off → Bad.
func TestWALFsyncOff(t *testing.T) {
	s := healthyWALSettings()
	s["fsync"] = "off"
	got := runWAL(s, 16, 0)
	if !hasLevel(got, pgtuner.Bad) {
		t.Fatalf("expected Bad for fsync=off, got %v", levelsOf(got))
	}
}

// TestWALFullPageWritesOff: full_page_writes=off → Bad.
func TestWALFullPageWritesOff(t *testing.T) {
	s := healthyWALSettings()
	s["full_page_writes"] = "off"
	got := runWAL(s, 16, 0)
	if !hasLevel(got, pgtuner.Bad) {
		t.Fatalf("expected Bad for full_page_writes=off, got %v", levelsOf(got))
	}
}

// TestWALSynchronousCommitOff: synchronous_commit=off → Info.
func TestWALSynchronousCommitOff(t *testing.T) {
	s := healthyWALSettings()
	s["synchronous_commit"] = "off"
	got := runWAL(s, 16, 0)
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for synchronous_commit=off, got %v", levelsOf(got))
	}
}

// TestWALCompressionOffPG15: wal_compression=off on PG15 → Info mentioning lz4/zstd.
func TestWALCompressionOffPG15(t *testing.T) {
	s := healthyWALSettings()
	s["wal_compression"] = "off"
	got := runWAL(s, 15, 0)
	found := false
	for _, a := range got {
		if a.Level == pgtuner.Info && strings.Contains(a.Detail, "lz4") && strings.Contains(a.Detail, "zstd") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Info with lz4/zstd mention for PG15 wal_compression=off, got %v", got)
	}
}

// TestWALCompressionOffPG14: wal_compression=off on PG14 → Info mentioning lz4 but NOT zstd.
func TestWALCompressionOffPG14(t *testing.T) {
	s := healthyWALSettings()
	s["wal_compression"] = "off"
	got := runWAL(s, 14, 0)
	found := false
	for _, a := range got {
		if a.Level == pgtuner.Info && strings.Contains(a.Detail, "lz4") {
			if strings.Contains(a.Detail, "zstd") {
				t.Fatalf("PG14 advice should NOT mention zstd, got detail: %q", a.Detail)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Info with lz4 mention for PG14 wal_compression=off, got %v", got)
	}
}

// TestWALCompressionOffPrePG14: wal_compression=off on PG13 → Info without lz4/zstd.
func TestWALCompressionOffPrePG14(t *testing.T) {
	s := healthyWALSettings()
	s["wal_compression"] = "off"
	got := runWAL(s, 13, 0)
	found := false
	for _, a := range got {
		if a.Level == pgtuner.Info && strings.Contains(a.Message, "wal_compression") {
			if strings.Contains(a.Detail, "lz4") || strings.Contains(a.Detail, "zstd") {
				t.Fatalf("pre-PG14 advice should NOT mention lz4 or zstd, got detail: %q", a.Detail)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Info for pre-PG14 wal_compression=off, got %v", got)
	}
}

// TestWALLevelMinimal: wal_level=minimal → Info.
func TestWALLevelMinimal(t *testing.T) {
	s := healthyWALSettings()
	s["wal_level"] = "minimal"
	got := runWAL(s, 16, 0)
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for wal_level=minimal, got %v", levelsOf(got))
	}
}

// TestWALAllHealthy: healthy settings → single OK.
func TestWALAllHealthy(t *testing.T) {
	got := runWAL(healthyWALSettings(), 16, 0)
	if !hasLevel(got, pgtuner.OK) {
		t.Fatalf("expected OK for all-healthy WAL settings, got %v", levelsOf(got))
	}
	for _, a := range got {
		if a.Level == pgtuner.Bad || a.Level == pgtuner.Warn {
			t.Fatalf("unexpected high-severity advice for healthy settings: %v", a)
		}
	}
}
