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

func TestStorageIntegration(t *testing.T) {
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

	// Cluster-wide settings half.
	gotSettings := StorageSettingsCheck{}.Run(ctx, probe)
	if len(gotSettings) == 0 {
		t.Fatal("expected at least one advice from StorageSettingsCheck, got none")
	}
	for _, a := range gotSettings {
		if a.Level == pgtuner.Unknown {
			t.Fatalf("StorageSettingsCheck returned Unknown: %v", a)
		}
	}
	t.Logf("StorageSettingsCheck returned %d advice item(s): %v", len(gotSettings), levelsOf(gotSettings))

	// Per-database temp-file stats half.
	got := StorageCheck{}.Run(ctx, probe)
	if len(got) == 0 {
		t.Fatal("expected at least one advice from StorageCheck, got none")
	}
	// Must not contain Unknown from a query error.
	for _, a := range got {
		if a.Level == pgtuner.Unknown {
			t.Fatalf("StorageCheck returned Unknown (query failure): %v", a)
		}
	}
	t.Logf("StorageCheck returned %d advice item(s): %v", len(got), levelsOf(got))
}
