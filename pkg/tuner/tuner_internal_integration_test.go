//go:build integration

package tuner

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/OpenRemedy/pgtuner/internal/checks"
	"github.com/OpenRemedy/pgtuner/internal/osinspect"
	"github.com/OpenRemedy/pgtuner/internal/pg"
)

// TestAnalyzeDatabaseConnectFailure exercises the real per-database
// connect-failure path: pointing analyzeDatabase at a database that does not
// exist must return an error (which Analyze turns into a tagged Unknown via
// dbFailureAdvice) rather than panicking or hanging. This covers the branch
// that the all-databases unit/integration tests otherwise cannot reach against
// a healthy cluster where every database connects cleanly.
func TestAnalyzeDatabaseConnectFailure(t *testing.T) {
	dsn := os.Getenv("PGTUNER_TEST_DSN")
	if dsn == "" {
		t.Skip("PGTUNER_TEST_DSN not set")
	}

	ctx := context.Background()
	const bogusDB = "pgtuner_does_not_exist_db_xyz"

	advice, err := analyzeDatabase(ctx, dsn, bogusDB, osinspect.NoopInspector{}, time.Time{}, pg.Role{}, checks.PerDatabase())
	if err == nil {
		t.Fatalf("expected analyzeDatabase to fail for nonexistent database %q, got advice=%v", bogusDB, advice)
	}
	if advice != nil {
		t.Errorf("expected nil advice on failure, got %v", advice)
	}

	// The failure is surfaced to the operator as a tagged Unknown.
	a := dbFailureAdvice(bogusDB, err)
	if a.Database != bogusDB {
		t.Errorf("Database tag: got %q, want %q", a.Database, bogusDB)
	}
	t.Logf("connect-failure surfaced as: %s — %s", a.Title, a.Message)
}
