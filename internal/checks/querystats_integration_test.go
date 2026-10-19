//go:build integration

package checks

import (
	"context"
	"os"
	"testing"

	"github.com/soylic/pgtuner/internal/osinspect"
	"github.com/soylic/pgtuner/internal/pg"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// Runs QueryStatsCheck against a dev PG where pg_stat_statements is NOT installed.
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

	// The test is valid only if pg_stat_statements is absent.
	var extCount int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM pg_extension WHERE extname='pg_stat_statements'").Scan(&extCount); err != nil {
		t.Fatalf("check extension presence: %v", err)
	}
	if extCount > 0 {
		t.Skip("pg_stat_statements IS installed on this DB, integration absent-path test requires it to be absent")
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

	if len(got) != 1 {
		t.Fatalf("expected exactly 1 advice (absent-path Info), got %d: %+v", len(got), got)
	}

	a := got[0]
	if a.Level != pgtuner.Info {
		t.Errorf("expected Info level for absent-path advice, got %s: %+v", a.Level, a)
	}

	if !containsStr(a.Title, "pg_stat_statements") && !containsStr(a.Message, "pg_stat_statements") {
		t.Errorf("expected 'pg_stat_statements' in advice title or message, got title=%q message=%q",
			a.Title, a.Message)
	}

	for _, item := range got {
		if item.Level == pgtuner.Unknown {
			t.Errorf("QueryStatsCheck returned Unknown (query failure): %+v", item)
		}
	}

	t.Logf("QueryStatsCheck absent-path: [%s] %s: %s", a.Level, a.Section, a.Title)
}
