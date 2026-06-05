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

func TestReplicationIntegration(t *testing.T) {
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
	t.Logf("DetectRole returned role=%q InRecovery=%v ConnectedStandbys=%d",
		role.Role, role.InRecovery, role.ConnectedStandbys)

	probe := &Probe{
		Conn:     conn,
		Settings: settings,
		Version:  conn.Version,
		OS:       osinspect.NoopInspector{},
		Role:     role,
	}

	got := ReplicationCheck{}.Run(ctx, probe)
	if len(got) == 0 {
		t.Fatal("expected at least one advice, got none")
	}
	// No query-failure Unknowns expected against a healthy PG16 instance.
	for _, a := range got {
		if a.Level == pgtuner.Unknown {
			t.Errorf("ReplicationCheck returned Unknown (query failure): %+v", a)
		}
	}
	t.Logf("ReplicationCheck returned %d advice item(s): %v", len(got), levelsOf(got))

	// The dev PG16 is standalone → the first advice must be the Standalone role Info.
	if role.Role == pg.RoleStandalone {
		found := false
		for _, a := range got {
			if a.Level == pgtuner.Info && a.Title == "This server is standalone (no replication)" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected standalone role Info in output, got: %v", levelsOf(got))
		}
	}

	t.Logf("server version: %s", conn.Version)
}
