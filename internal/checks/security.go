package checks

import (
	"context"
	"fmt"

	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// src: https://www.postgresql.org/docs/current/role-attributes.html
const docRoleAttributes = "https://www.postgresql.org/docs/current/role-attributes.html"

// src: https://www.postgresql.org/docs/current/auth-password.html
const docAuthPassword = "https://www.postgresql.org/docs/current/auth-password.html"

// PG15 revoked CREATE on the public schema from the PUBLIC pseudo-role.
// src: https://www.postgresql.org/docs/15/ddl-schemas.html
const docDDLSchemas = "https://www.postgresql.org/docs/15/ddl-schemas.html"

// allow_alter_system was added in PG17.
// src: https://www.postgresql.org/docs/17/runtime-config-client.html
const docAlterSystem17 = "https://www.postgresql.org/docs/17/runtime-config-client.html"

// evalSuperuserCount returns advice when more than one superuser exists. The
// installation superuser is usually the only one needed.
func evalSuperuserCount(n int64) *pgtuner.Advice {
	if n <= 1 {
		return nil
	}
	a := pgtuner.Advice{
		Level:   pgtuner.Warn,
		Section: "Security",
		Title:   fmt.Sprintf("%d superuser roles exist. Reduce the number of superuser accounts.", n),
		Message: fmt.Sprintf("pg_roles shows %d roles with the SUPERUSER attribute. Superusers bypass all access controls. Each additional superuser adds an unnecessary risk of privilege escalation.", n),
		Detail:  "Audit the superuser roles. Remove the grants that are not necessary. Use GRANT … WITH ADMIN OPTION or role memberships to delegate only the privileges that are needed.",
		DocRef:  docRoleAttributes,
	}
	return &a
}

// SecurityCheck evaluates superuser count, password hashing, and public-schema
// CREATE privileges.
type SecurityCheck struct{ clusterScoped }

func (SecurityCheck) Name() string { return "security" }

// Run evaluates each sub-check independently. A query failure yields one
// Unknown advice for that sub-check and does not block the others.
func (SecurityCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "Security"
	var out []pgtuner.Advice

	const superuserSQL = "SELECT count(*) FROM pg_roles WHERE rolsuper"
	var superuserCount int64
	if err := p.Conn.QueryRow(ctx, superuserSQL).Scan(&superuserCount); err != nil {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Superuser count unavailable",
			Message: fmt.Sprintf("Could not query pg_roles for the superuser count: %v", err),
		})
	} else if a := evalSuperuserCount(superuserCount); a != nil {
		out = append(out, *a)
	}

	// md5 hashes are brute-forceable on modern GPUs. scram-sha-256 (default since PG14) uses a PBKDF.
	// src: https://www.postgresql.org/docs/current/auth-password.html
	if pe, ok := p.Settings.Get("password_encryption"); ok && pe == "md5" {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Warn,
			Section: section,
			Title:   "md5 password authentication is weak. Use scram-sha-256.",
			Message: "password_encryption=md5. Attackers can brute-force MD5-hashed passwords easily. They are not considered secure. scram-sha-256 has been the default since PostgreSQL 14.",
			Detail:  "Set password_encryption=scram-sha-256. Then reset the passwords of all roles that use md5. Update the pg_hba.conf entries to match.",
			DocRef:  docAuthPassword,
		})
	}

	// Since PG15 PUBLIC has no CREATE on the public schema by default. If it does,
	// the cluster may have been upgraded without the recommended REVOKE.
	// src: https://www.postgresql.org/docs/15/ddl-schemas.html
	if p.Version.AtLeast(15, 0) {
		const publicCreateSQL = "SELECT has_schema_privilege('public', 'public', 'CREATE')"
		var publicCreate bool
		if err := p.Conn.QueryRow(ctx, publicCreateSQL).Scan(&publicCreate); err != nil {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Unknown,
				Section: section,
				Title:   "Public-schema privilege check unavailable",
				Message: fmt.Sprintf("Could not query has_schema_privilege for the CREATE privilege on the public schema: %v", err),
			})
		} else if publicCreate {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Warn,
				Section: section,
				Title:   "PUBLIC retains CREATE on schema public",
				Message: "The PUBLIC pseudo-role still has the CREATE privilege on the public schema. PostgreSQL 15 and later revoke it by default. While it remains, any authenticated user can create objects in the public schema.",
				Detail:  "Run: REVOKE CREATE ON SCHEMA public FROM PUBLIC;",
				DocRef:  docDDLSchemas,
			})
		}
	}

	// ALTER SYSTEM writes postgresql.auto.conf and can override any server setting.
	// Setting off limits what a compromised superuser can change persistently.
	// src: https://www.postgresql.org/docs/17/runtime-config-client.html
	if p.Version.AtLeast(17, 0) {
		if aas, ok := p.Settings.Get("allow_alter_system"); ok && aas == "on" {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Info,
				Section: section,
				Title:   "allow_alter_system is on (default). Consider disabling it to harden the server (PG17+).",
				Message: "allow_alter_system=on. Any superuser can run ALTER SYSTEM and write persistent overrides to postgresql.auto.conf. Setting it to off prevents accidental or malicious persistent configuration changes.",
				Detail:  "Set allow_alter_system=off in postgresql.conf to prevent ALTER SYSTEM. After that, edit postgresql.conf directly through filesystem access to change settings.",
				DocRef:  docAlterSystem17,
			})
		}
	}

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.OK,
			Section: section,
			Title:   "Security configuration looks healthy",
			Message: "No high-priority security concerns detected. The superuser count is minimal. Password encryption is strong. The CREATE privilege on the public schema is revoked.",
			DocRef:  docRoleAttributes,
		})
	}
	return out
}
