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

// TestConfigIntegration runs ConfigCheck against the live dev DB and asserts no
// sub-check returns Unknown (every query path must succeed against the live
// schema).  It logs every advice item for inspection.
func TestConfigIntegration(t *testing.T) {
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

	got := ConfigCheck{}.Run(ctx, probe)
	if len(got) == 0 {
		t.Fatal("expected at least one advice, got none")
	}

	for _, a := range got {
		t.Logf("[%s] %s: %s", a.Level, a.Section, a.Title)
		if a.Section != configSection {
			t.Errorf("advice in wrong section %q: %+v", a.Section, a)
		}
		if a.Level == pgtuner.Unknown {
			t.Errorf("ConfigCheck returned Unknown (query failure): %+v", a)
		}
	}
}
