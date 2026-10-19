package pg

import (
	"context"
	"fmt"
	"strings"

	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// flavorSignals is what DetectFlavor reads from the server. Splitting it out
// lets classifyFlavor run without a database.
type flavorSignals struct {
	versionString string   // SELECT version()
	serverVersion string   // SHOW server_version
	prefixes      []string // setting-name prefixes present: rds., cloudsql., azure., neon., alloydb.
	roles         []string // provider roles present
	extensions    []string // distribution extensions present
	auroraVersion bool     // aurora_version() answered
}

// Setting-name prefixes and roles that identify a hosted service. These are
// observed conventions of each provider, not a documented contract.
var (
	flavorPrefixes = []string{"rds.", "cloudsql.", "azure.", "neon.", "alloydb."}
	flavorRoles    = []string{"rdsadmin", "cloudsqladmin", "azure_superuser", "supabase_admin", "neon_superuser"}
	flavorExts     = []string{"timescaledb", "citus"}
)

// DetectFlavor identifies the distribution behind the connection. Every probe is
// best-effort, so the result falls back to plain "postgresql".
func DetectFlavor(ctx context.Context, c *Conn, s *Settings) pgtuner.Flavor {
	var sig flavorSignals
	_ = c.QueryRow(ctx, "SELECT version()").Scan(&sig.versionString)
	sig.serverVersion, _ = s.Get("server_version")
	for _, p := range flavorPrefixes {
		if s.HasPrefix(p) {
			sig.prefixes = append(sig.prefixes, p)
		}
	}
	sig.roles = queryStrings(ctx, c, "SELECT rolname FROM pg_roles WHERE rolname = ANY($1)", flavorRoles)
	sig.extensions = queryStrings(ctx, c, "SELECT extname FROM pg_extension WHERE extname = ANY($1)", flavorExts)
	var v string
	sig.auroraVersion = c.QueryRow(ctx, "SELECT aurora_version()").Scan(&v) == nil
	return classifyFlavor(sig)
}

func queryStrings(ctx context.Context, c *Conn, sql string, arg []string) []string {
	rows, err := c.Query(ctx, sql, arg)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			break
		}
		out = append(out, s)
	}
	return out
}

// classifyFlavor picks the most specific match: a forked engine first, then a
// hosted service, then plain PostgreSQL.
func classifyFlavor(sig flavorSignals) pgtuner.Flavor {
	f := pgtuner.Flavor{Name: "postgresql", Extensions: sig.extensions}
	f.Packaging = packagingOf(sig.serverVersion)

	has := func(list []string, v string) bool {
		for _, x := range list {
			if x == v {
				return true
			}
		}
		return false
	}
	set := func(name string, managed bool, evidence string) {
		f.Name, f.Managed = name, managed
		f.Evidence = append(f.Evidence, evidence)
	}

	vs := sig.versionString
	switch {
	case strings.Contains(vs, "CockroachDB"):
		set("cockroachdb", false, "version() mentions CockroachDB")
	case strings.Contains(vs, "-YB-"):
		set("yugabytedb", false, "version() contains -YB-")
	case strings.Contains(vs, "Greenplum"):
		set("greenplum", false, "version() mentions Greenplum")
	case strings.Contains(vs, "EnterpriseDB") || strings.Contains(sig.serverVersion, "EDB"):
		set("edb", false, "version() or server_version mentions EDB")
	case sig.auroraVersion:
		set("aurora", true, "aurora_version() exists")
	case has(sig.prefixes, "alloydb."):
		set("alloydb", true, "settings with prefix alloydb.")
	case has(sig.prefixes, "rds.") || has(sig.roles, "rdsadmin"):
		set("rds", true, "settings with prefix rds. or role rdsadmin")
	case has(sig.prefixes, "cloudsql.") || has(sig.roles, "cloudsqladmin"):
		set("cloudsql", true, "settings with prefix cloudsql. or role cloudsqladmin")
	case has(sig.prefixes, "azure.") || has(sig.roles, "azure_superuser"):
		set("azure", true, "settings with prefix azure. or role azure_superuser")
	case has(sig.roles, "supabase_admin"):
		set("supabase", true, "role supabase_admin exists")
	case has(sig.prefixes, "neon.") || has(sig.roles, "neon_superuser"):
		set("neon", true, "settings with prefix neon. or role neon_superuser")
	}
	if len(sig.extensions) > 0 {
		f.Evidence = append(f.Evidence, fmt.Sprintf("extensions: %s", strings.Join(sig.extensions, ", ")))
	}
	return f
}

// packagingOf returns the text in parentheses of server_version, if any.
// "16.2 (Debian 16.2-1.pgdg120+2)" gives "Debian 16.2-1.pgdg120+2".
func packagingOf(serverVersion string) string {
	i := strings.Index(serverVersion, "(")
	j := strings.LastIndex(serverVersion, ")")
	if i < 0 || j <= i {
		return ""
	}
	return serverVersion[i+1 : j]
}
