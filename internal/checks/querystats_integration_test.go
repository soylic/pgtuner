//go:build integration

package checks

import (
	"context"
	"os"
	"testing"

	"github.com/OpenRemedy/pgtuner/internal/osinspect"
	"github.com/OpenRemedy/pgtuner/internal/pg"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// TestQueryStatsIntegration_ExtensionAbsent runs QueryStatsCheck against the dev
// PG instance where pg_stat_statements is NOT installed.  It asserts:
//  1. Exactly one advice item is returned.
//  2. The advice level is Info (not Unknown — no query failure).
//  3. The advice title mentions pg_stat_statements.
//  4. No Unknown-level advice is present (all sub-check queries succeeded).
//
// This verifies the "absent" path works cleanly without any Unknown fallthrough.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
func TestQueryStatsIntegration_ExtensionAbsent(t *testing.T) {
	dsn := os.Getenv("PGTUNER_TEST_DSN")
	if dsn == "" {
		t.Skip("set PGTUNER_TEST_DSN to run integration tests")
	}

	ctx := context.Background()
	conn, err := pg.Connect(ctx, pg.Options{DSN: dsn})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	// Verify the assumption: pg_stat_statements must NOT be installed on the
	// dev DB for this test to be valid.
	var extCount int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM pg_extension WHERE extname='pg_stat_statements'").Scan(&extCount); err != nil {
		t.Fatalf("check extension presence: %v", err)
	}
	if extCount > 0 {
		t.Skip("pg_stat_statements IS installed on this DB — integration absent-path test requires it to be absent")
	}

	settings, err := pg.LoadSettings(ctx, conn)
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}

	role, err := pg.DetectRole(ctx, conn)
	if err != nil {
		t.Fatalf("DetectRole: %v", err)
	}

	probe := &Probe{
		Conn:     conn,
		Settings: settings,
		Version:  conn.Version,
		OS:       osinspect.NoopInspector{},
		Role:     role,
	}

	got := QueryStatsCheck{}.Run(ctx, probe)

	// Assertion 1: exactly one advice item (the "not installed" Info).
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 advice (absent-path Info), got %d: %+v", len(got), got)
	}

	// Assertion 2: the advice level must be Info, not Unknown.
	a := got[0]
	if a.Level != pgtuner.Info {
		t.Errorf("expected Info level for absent-path advice, got %s: %+v", a.Level, a)
	}

	// Assertion 3: the advice title or message must mention pg_stat_statements.
	if !containsStr(a.Title, "pg_stat_statements") && !containsStr(a.Message, "pg_stat_statements") {
		t.Errorf("expected 'pg_stat_statements' in advice title or message, got title=%q message=%q",
			a.Title, a.Message)
	}

	// Assertion 4: no Unknown-level advice.
	for _, item := range got {
		if item.Level == pgtuner.Unknown {
			t.Errorf("QueryStatsCheck returned Unknown (query failure): %+v", item)
		}
	}

	t.Logf("QueryStatsCheck absent-path: [%s] %s — %s", a.Level, a.Section, a.Title)
}
