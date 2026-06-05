//go:build integration

package tuner_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
	"github.com/OpenRemedy/pgtuner/pkg/tuner"
)

func TestAnalyzeIntegration(t *testing.T) {
	dsn := os.Getenv("PGTUNER_TEST_DSN")
	if dsn == "" {
		t.Skip("PGTUNER_TEST_DSN not set")
	}

	ctx := context.Background()
	rep, err := tuner.Analyze(ctx, pgtuner.Options{DSN: dsn})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if !strings.HasPrefix(rep.ServerVersion, "16") {
		t.Errorf("ServerVersion: got %q, want prefix %q", rep.ServerVersion, "16")
	}

	if len(rep.Advice) == 0 {
		t.Error("Advice: got empty slice, want at least one entry")
	}

	if count, ok := rep.Summary["unknown"]; ok && count > 0 {
		t.Errorf("Summary: got %d unknown-level entries, want 0", count)
	}

	// A default (single-DB) run must not tag any advice with a database.
	for _, a := range rep.Advice {
		if a.Database != "" {
			t.Errorf("default run tagged advice with database %q: %q", a.Database, a.Title)
		}
	}
	if len(rep.Databases) != 0 {
		t.Errorf("default run populated Databases: %v", rep.Databases)
	}
}

func TestAnalyzeAllDatabasesIntegration(t *testing.T) {
	dsn := os.Getenv("PGTUNER_TEST_DSN")
	if dsn == "" {
		t.Skip("PGTUNER_TEST_DSN not set")
	}

	ctx := context.Background()
	rep, err := tuner.Analyze(ctx, pgtuner.Options{DSN: dsn, AllDatabases: true})
	if err != nil {
		t.Fatalf("Analyze(-all-databases): %v", err)
	}

	// The dev cluster has at least one user database.
	if len(rep.Databases) == 0 {
		t.Fatal("Databases: got empty, want at least one user database")
	}

	// No Unknown anywhere (every per-DB query path must succeed).
	if count, ok := rep.Summary["unknown"]; ok && count > 0 {
		t.Errorf("Summary: got %d unknown-level entries, want 0", count)
	}

	// Per-database checks must be tagged with a database in rep.Databases;
	// cluster-wide checks must remain untagged. At least one tagged item must
	// exist (per-DB checks always emit something, e.g. OK).
	dbSet := make(map[string]bool, len(rep.Databases))
	for _, d := range rep.Databases {
		dbSet[d] = true
	}
	tagged := 0
	for _, a := range rep.Advice {
		if a.Database == "" {
			continue
		}
		tagged++
		if !dbSet[a.Database] {
			t.Errorf("advice tagged with unknown database %q", a.Database)
		}
	}
	if tagged == 0 {
		t.Error("expected at least one per-database-tagged advice item, got none")
	}

	// Sanity: the connection database 'openremedy' should be present.
	if !dbSet["openremedy"] {
		t.Errorf("expected 'openremedy' in Databases, got %v", rep.Databases)
	}
}
