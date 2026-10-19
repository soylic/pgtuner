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

func TestDefaultRegistryIntegration(t *testing.T) {
	dsn := os.Getenv("PGTUNER_TEST_DSN")
	if dsn == "" {
		t.Skip("set PGTUNER_TEST_DSN")
	}

	ctx := context.Background()
	conn, err := pg.Connect(ctx, pg.Options{DSN: dsn})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	settings, err := pg.LoadSettings(ctx, conn)
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}

	role, err := pg.DetectRole(ctx, conn)
	if err != nil {
		t.Logf("DetectRole failed (non-fatal): %v", err)
	}

	probe := &Probe{
		Conn:     conn,
		Settings: settings,
		Version:  conn.Version,
		OS:       osinspect.NoopInspector{},
		Role:     role,
		Topology: pg.DetectTopology(ctx, conn, role, settings),
		Flavor:   pg.DetectFlavor(ctx, conn, settings),
	}

	registry := Default()
	if len(registry) != 23 {
		t.Fatalf("Default() registry has %d checks, want 23", len(registry))
	}

	type result struct {
		name   string
		advice []pgtuner.Advice
	}
	var results []result

	for _, check := range registry {
		advice := check.Run(ctx, probe)
		results = append(results, result{name: check.Name(), advice: advice})
	}

	t.Logf("=== Default registry results against PG%s ===", conn.Version)
	for _, r := range results {
		if len(r.advice) == 0 {
			t.Errorf("check %q returned no advice items", r.name)
			continue
		}
		for _, a := range r.advice {
			if a.Level == pgtuner.Unknown {
				t.Errorf("check %q returned Unknown: section=%q title=%q message=%q",
					r.name, a.Section, a.Title, a.Message)
			}
		}
		t.Logf("  %-14s → %v", r.name, levelsOf(r.advice))
	}
}
