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

func TestStatisticsIntegration(t *testing.T) {
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

	got := StatisticsCheck{}.Run(ctx, probe)
	if len(got) == 0 {
		t.Fatal("expected at least one advice, got none")
	}
	// StatisticsCheck is settings-only; Unknown indicates a bug in the check.
	for _, a := range got {
		if a.Level == pgtuner.Unknown {
			t.Fatalf("StatisticsCheck returned Unknown: %v", a)
		}
	}
	t.Logf("StatisticsCheck returned %d advice item(s): %v", len(got), levelsOf(got))
}
