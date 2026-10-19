//go:build integration

package tuner

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/soylic/pgtuner/internal/checks"
	"github.com/soylic/pgtuner/internal/osinspect"
	"github.com/soylic/pgtuner/internal/pg"
)

// TestAnalyzeDatabaseConnectFailure checks that a missing database returns an
// error, not a panic or hang. Analyze turns it into an Unknown via dbFailureAdvice.
func TestAnalyzeDatabaseConnectFailure(t *testing.T) {
	dsn := os.Getenv("PGTUNER_TEST_DSN")
	if dsn == "" {
		t.Skip("PGTUNER_TEST_DSN not set")
	}

	ctx := context.Background()
	const bogusDB = "pgtuner_does_not_exist_db_xyz"

	advice, err := analyzeDatabase(ctx, pg.Options{DSN: dsn}, bogusDB, osinspect.NoopInspector{}, time.Time{}, pg.Role{}, checks.PerDatabase())
	if err == nil {
		t.Fatalf("expected analyzeDatabase to fail for nonexistent database %q, got advice=%v", bogusDB, advice)
	}
	if advice != nil {
		t.Errorf("expected nil advice on failure, got %v", advice)
	}

	a := dbFailureAdvice(bogusDB, err)
	if a.ID != "analysis.database-failed" {
		t.Errorf("ID: got %q, want analysis.database-failed", a.ID)
	}
	if a.Database != bogusDB {
		t.Errorf("Database tag: got %q, want %q", a.Database, bogusDB)
	}
	t.Logf("connect-failure surfaced as: %s: %s", a.Title, a.Message)
}
