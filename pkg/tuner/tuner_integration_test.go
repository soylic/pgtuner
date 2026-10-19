//go:build integration

package tuner_test

import (
	"context"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/soylic/pgtuner/pkg/pgtuner"
	"github.com/soylic/pgtuner/pkg/tuner"
)

// serverVersionRE matches major.minor. The CI matrix runs PG 14-18, so no single major is pinned.
var serverVersionRE = regexp.MustCompile(`^\d+\.\d+`)

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

	if !serverVersionRE.MatchString(rep.ServerVersion) {
		t.Errorf("ServerVersion: got %q, want non-empty major.minor shape (e.g. %q)", rep.ServerVersion, "16.13")
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

	// The database we connected to must appear in Databases. Derive it from the
	// DSN so the test passes against any database name.
	if connDB := dbFromDSN(dsn); connDB != "" && !dbSet[connDB] {
		t.Errorf("expected connection database %q in Databases, got %v", connDB, rep.Databases)
	}
}

// dbFromDSN extracts the database name from a URL or keyword DSN. It returns "" if none is found.
func dbFromDSN(dsn string) string {
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		return strings.TrimPrefix(u.Path, "/")
	}
	for _, field := range strings.Fields(dsn) {
		if name, ok := strings.CutPrefix(field, "dbname="); ok {
			return name
		}
	}
	return ""
}
