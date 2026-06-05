package checks

import (
	"testing"
)

func TestDefaultRegistry(t *testing.T) {
	r := Default()

	// Must contain exactly 22 checks.
	if len(r) != 22 {
		t.Fatalf("Default() returned %d checks, want 22", len(r))
	}

	// Every check must have a non-empty, unique name.
	seen := make(map[string]bool, len(r))
	for i, c := range r {
		name := c.Name()
		if name == "" {
			t.Errorf("check at index %d has empty Name()", i)
		}
		if seen[name] {
			t.Errorf("duplicate check name %q at index %d", name, i)
		}
		seen[name] = true
	}
}

func TestRegistryScopeSplit(t *testing.T) {
	def := Default()
	cluster := ClusterWide()
	perDB := PerDatabase()

	// Counts must add up to the full registry size with no overlap.
	if len(cluster)+len(perDB) != len(def) {
		t.Fatalf("cluster(%d)+perDB(%d) != Default(%d)", len(cluster), len(perDB), len(def))
	}
	if len(def) != 22 {
		t.Fatalf("Default() has %d checks, want 22", len(def))
	}
	if len(cluster) != 16 {
		t.Fatalf("ClusterWide() has %d checks, want 16", len(cluster))
	}

	// Every cluster check is ClusterScope; every perDB check is DatabaseScope.
	for _, c := range cluster {
		if c.Scope() != ClusterScope {
			t.Errorf("ClusterWide() contains non-cluster check %q", c.Name())
		}
	}
	for _, c := range perDB {
		if c.Scope() != DatabaseScope {
			t.Errorf("PerDatabase() contains non-database check %q", c.Name())
		}
	}

	// No overlap by name, and the union equals Default() exactly (same names).
	union := make(map[string]bool)
	for _, c := range cluster {
		if union[c.Name()] {
			t.Errorf("duplicate across split: %q", c.Name())
		}
		union[c.Name()] = true
	}
	for _, c := range perDB {
		if union[c.Name()] {
			t.Errorf("check %q appears in both ClusterWide and PerDatabase", c.Name())
		}
		union[c.Name()] = true
	}
	for _, c := range def {
		if !union[c.Name()] {
			t.Errorf("Default() check %q missing from union of split", c.Name())
		}
	}
	if len(union) != len(def) {
		t.Errorf("union has %d unique names, want %d", len(union), len(def))
	}

	// The expected per-database set per the design. The cluster-wide settings
	// halves of storage and autovacuum (storage_settings, autovacuum_settings)
	// are NOT here — only the genuinely per-database stats checks are.
	wantPerDB := map[string]bool{
		"cache": true, "schema": true, "storage": true,
		"autovacuum": true, "workload": true, "querystats": true,
	}
	if len(perDB) != len(wantPerDB) {
		t.Fatalf("PerDatabase() has %d checks, want %d", len(perDB), len(wantPerDB))
	}
	for _, c := range perDB {
		if !wantPerDB[c.Name()] {
			t.Errorf("unexpected per-database check %q", c.Name())
		}
	}
}
