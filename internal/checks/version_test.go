package checks

import (
	"context"
	"testing"
	"time"

	"github.com/OpenRemedy/pgtuner/internal/osinspect"
	"github.com/OpenRemedy/pgtuner/internal/pg"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

func runVersion(major, minor int, now time.Time) []pgtuner.Advice {
	p := &Probe{
		Settings: pg.NewSettings(nil),
		OS:       osinspect.NoopInspector{},
		Version:  pg.ServerVersion{Major: major, Minor: minor},
		Now:      now,
	}
	return VersionCheck{}.Run(context.Background(), p)
}

// TestVersionPastEOL verifies PG13 with Now=2026-06-03 is flagged Bad.
// EOL for PG13 was 2025-11-13; the test date is past that.
func TestVersionPastEOL(t *testing.T) {
	now := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	got := runVersion(13, 18, now)
	if len(got) != 1 || got[0].Level != pgtuner.Bad {
		t.Fatalf("expected Bad for PG13 past EOL, got %v", levelsOf(got))
	}
}

// TestVersionNearEOL verifies PG14 with Now=2026-06-03 is flagged Warn.
// EOL for PG14 is 2026-11-12; the test date is within 12 months.
func TestVersionNearEOL(t *testing.T) {
	now := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	got := runVersion(14, 10, now)
	if len(got) != 1 || got[0].Level != pgtuner.Warn {
		t.Fatalf("expected Warn for PG14 near EOL, got %v", levelsOf(got))
	}
}

// TestVersionHealthy verifies PG17 with Now=2026-06-03 is flagged OK.
// EOL for PG17 is 2029-11-08; the test date is > 12 months away.
func TestVersionHealthy(t *testing.T) {
	now := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	got := runVersion(17, 4, now)
	if len(got) != 1 || got[0].Level != pgtuner.OK {
		t.Fatalf("expected OK for PG17 with plenty of support left, got %v", levelsOf(got))
	}
}

// TestVersionUnknownMajor verifies that an unlisted major (PG99) returns Info.
func TestVersionUnknownMajor(t *testing.T) {
	now := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	got := runVersion(99, 0, now)
	if len(got) != 1 || got[0].Level != pgtuner.Info {
		t.Fatalf("expected Info for unknown major PG99, got %v", levelsOf(got))
	}
}
