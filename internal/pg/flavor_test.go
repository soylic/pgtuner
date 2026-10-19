package pg

import "testing"

func TestClassifyFlavor(t *testing.T) {
	tests := []struct {
		name        string
		sig         flavorSignals
		wantName    string
		wantManaged bool
	}{
		{"vanilla", flavorSignals{versionString: "PostgreSQL 17.5 on x86_64"}, "postgresql", false},
		{"cockroach", flavorSignals{versionString: "PostgreSQL 13.0.0 (CockroachDB CCL v23.1)"}, "cockroachdb", false},
		{"yugabyte", flavorSignals{versionString: "PostgreSQL 11.2-YB-2.18.0.0-b0"}, "yugabytedb", false},
		{"aurora wins over rds", flavorSignals{prefixes: []string{"rds."}, roles: []string{"rdsadmin"}, auroraVersion: true}, "aurora", true},
		{"rds by prefix", flavorSignals{prefixes: []string{"rds."}}, "rds", true},
		{"rds by role", flavorSignals{roles: []string{"rdsadmin"}}, "rds", true},
		{"cloudsql", flavorSignals{prefixes: []string{"cloudsql."}}, "cloudsql", true},
		{"azure", flavorSignals{roles: []string{"azure_superuser"}}, "azure", true},
		{"supabase", flavorSignals{roles: []string{"supabase_admin"}}, "supabase", true},
		{"neon", flavorSignals{prefixes: []string{"neon."}}, "neon", true},
		{"timescale stays postgresql", flavorSignals{extensions: []string{"timescaledb"}}, "postgresql", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyFlavor(tt.sig)
			if got.Name != tt.wantName || got.Managed != tt.wantManaged {
				t.Errorf("got %s managed=%v, want %s managed=%v", got.Name, got.Managed, tt.wantName, tt.wantManaged)
			}
		})
	}
}

func TestPackagingOf(t *testing.T) {
	tests := map[string]string{
		"16.2 (Debian 16.2-1.pgdg120+2)":      "Debian 16.2-1.pgdg120+2",
		"17.5":                                "",
		"14.9 (Ubuntu 14.9-0ubuntu0.22.04.1)": "Ubuntu 14.9-0ubuntu0.22.04.1",
		"broken (":                            "",
	}
	for in, want := range tests {
		if got := packagingOf(in); got != want {
			t.Errorf("packagingOf(%q) = %q, want %q", in, got, want)
		}
	}
}
