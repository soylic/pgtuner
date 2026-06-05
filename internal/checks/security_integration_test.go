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

func TestSecurityIntegration(t *testing.T) {
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

	probe := &Probe{
		Conn:     conn,
		Settings: settings,
		Version:  conn.Version,
		OS:       osinspect.NoopInspector{},
	}

	got := SecurityCheck{}.Run(ctx, probe)
	if len(got) == 0 {
		t.Fatal("expected at least one advice, got none")
	}
	// No query-failure Unknowns expected against a healthy PG16 instance.
	for _, a := range got {
		if a.Level == pgtuner.Unknown {
			t.Fatalf("SecurityCheck returned Unknown (query failure): %+v", a)
		}
	}
	t.Logf("SecurityCheck returned %d advice item(s): %v", len(got), levelsOf(got))

	// PG16: the public schema CREATE-revoke (PG≥15 rule) should not return Unknown.
	// The live DB has the privilege revoked (has_schema_privilege returns false),
	// so no Warn should appear from that sub-check.
	t.Logf("server version: %s", conn.Version)
}
