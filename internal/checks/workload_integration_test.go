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

func TestWorkloadIntegration(t *testing.T) {
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
		t.Fatalf("DetectRole: %v", err)
	}

	probe := &Probe{
		Conn:     conn,
		Settings: settings,
		Version:  conn.Version,
		OS:       osinspect.NoopInspector{},
		Role:     role,
	}

	got := WorkloadCheck{}.Run(ctx, probe)

	// WorkloadCheck must never return an empty slice (at minimum OK fallthrough).
	if len(got) == 0 {
		t.Fatal("expected at least one advice from WorkloadCheck, got none")
	}

	// No query-failure Unknowns expected against a healthy PG instance.
	for _, a := range got {
		if a.Level == pgtuner.Unknown {
			t.Errorf("WorkloadCheck returned Unknown (query failure): %+v", a)
		}
	}

	t.Logf("WorkloadCheck returned %d advice item(s): %v", len(got), levelsOf(got))
	for _, a := range got {
		t.Logf("  [%s] %s: %s", a.Level, a.Section, a.Title)
	}
}
