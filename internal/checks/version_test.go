package checks

import (
	"context"
	"testing"
	"time"

	"github.com/soylic/pgtuner/internal/osinspect"
	"github.com/soylic/pgtuner/internal/pg"
	"github.com/soylic/pgtuner/pkg/pgtuner"
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

// PG13 reached EOL on 2025-11-13.
func TestVersionPastEOL(t *testing.T) {
	now := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	got := runVersion(13, 18, now)
	if len(got) != 1 || got[0].Level != pgtuner.Bad {
		t.Fatalf("expected Bad for PG13 past EOL, got %v", levelsOf(got))
	}
}

// PG14 EOL is 2026-11-12, within the 12-month warning window.
func TestVersionNearEOL(t *testing.T) {
	now := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	got := runVersion(14, 10, now)
	if len(got) != 1 || got[0].Level != pgtuner.Warn {
		t.Fatalf("expected Warn for PG14 near EOL, got %v", levelsOf(got))
	}
}

// PG17 EOL is 2029-11-08, beyond the 12-month warning window.
func TestVersionHealthy(t *testing.T) {
	now := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	got := runVersion(17, 4, now)
	if len(got) != 1 || got[0].Level != pgtuner.OK {
		t.Fatalf("expected OK for PG17 with plenty of support left, got %v", levelsOf(got))
	}
}

func TestVersionUnknownMajor(t *testing.T) {
	now := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	got := runVersion(99, 0, now)
	if len(got) != 1 || got[0].Level != pgtuner.Info {
		t.Fatalf("expected Info for unknown major PG99, got %v", levelsOf(got))
	}
}
