package checks

import (
	"context"
	"fmt"

	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// docRoleAttributes is the canonical reference for role attributes including SUPERUSER.
// src: https://www.postgresql.org/docs/current/role-attributes.html
const docRoleAttributes = "https://www.postgresql.org/docs/current/role-attributes.html"

// docAuthPassword is the canonical reference for password_encryption.
// src: https://www.postgresql.org/docs/current/auth-password.html
const docAuthPassword = "https://www.postgresql.org/docs/current/auth-password.html"

// docDDLSchemas is the canonical reference for schema privileges; PG15 revoked
// CREATE on public schema from the PUBLIC pseudo-role.
// src: https://www.postgresql.org/docs/15/ddl-schemas.html
const docDDLSchemas = "https://www.postgresql.org/docs/15/ddl-schemas.html"

// docAlterSystem17 references the allow_alter_system GUC added in PG17.
// src: https://www.postgresql.org/docs/17/runtime-config-client.html
const docAlterSystem17 = "https://www.postgresql.org/docs/17/runtime-config-client.html"

// evalSuperuserCount is the pure decision function for superuser-count advice.
// Having more than 1 superuser role increases the attack surface; the postgres
// (or installation) superuser is typically the only one needed.
// src: https://www.postgresql.org/docs/current/role-attributes.html
func evalSuperuserCount(n int64) *pgtuner.Advice {
	if n <= 1 {
		return nil
	}
	a := pgtuner.Advice{
		Level:   pgtuner.Warn,
		Section: "Security",
		Title:   fmt.Sprintf("%d superuser roles exist; minimize superuser accounts", n),
		Message: fmt.Sprintf("pg_roles shows %d roles with the SUPERUSER attribute. Superusers bypass all access controls; each additional one is an unnecessary privilege escalation risk.", n),
		Detail:  "Audit superuser roles and remove unnecessary grants. Use GRANT … WITH ADMIN OPTION or role memberships to delegate only the privileges actually needed.",
		DocRef:  docRoleAttributes,
	}
	return &a
}

// SecurityCheck evaluates superuser count, password hashing, and public-schema
// CREATE privileges.
type SecurityCheck struct{ clusterScoped }

func (SecurityCheck) Name() string { return "security" }

// Run checks security-relevant settings and queries role/schema metadata.
// Each sub-check is independent; a query failure produces a single Unknown
// advice for that sub-check and does not block the others.
// It never panics and never returns an error.
func (SecurityCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "Security"
	var out []pgtuner.Advice

	// --- superuser count (query) --------------------------------------------------
	// Count all roles with SUPERUSER attribute.
	// src: https://www.postgresql.org/docs/current/role-attributes.html
	const superuserSQL = "SELECT count(*) FROM pg_roles WHERE rolsuper"
	var superuserCount int64
	if err := p.Conn.QueryRow(ctx, superuserSQL).Scan(&superuserCount); err != nil {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Superuser count unavailable",
			Message: fmt.Sprintf("Could not query pg_roles for superuser count: %v", err),
		})
	} else if a := evalSuperuserCount(superuserCount); a != nil {
		out = append(out, *a)
	}

	// --- password_encryption (setting) -------------------------------------------
	// md5 produces a salted MD5 hash that is trivially brute-forceable with modern
	// GPUs; scram-sha-256 (the default since PG14) uses a proper PBKDF.
	// src: https://www.postgresql.org/docs/current/auth-password.html
	if pe, ok := p.Settings.Get("password_encryption"); ok && pe == "md5" {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Warn,
			Section: section,
			Title:   "md5 password auth is weak; use scram-sha-256",
			Message: "password_encryption=md5. MD5-hashed passwords are easily brute-forced and are not considered secure. scram-sha-256 has been the default since PostgreSQL 14.",
			Detail:  "Set password_encryption=scram-sha-256, then reset passwords for all roles that use md5. Update pg_hba.conf entries accordingly.",
			DocRef:  docAuthPassword,
		})
	}

	// --- public schema CREATE privilege (PG≥15, query) ---------------------------
	// Since PG15 the PUBLIC pseudo-role no longer has CREATE on the public schema
	// by default. If it does have CREATE, this may indicate the cluster was upgraded
	// from an older version without running the recommended REVOKE.
	// src: https://www.postgresql.org/docs/15/ddl-schemas.html
	if p.Version.AtLeast(15, 0) {
		const publicCreateSQL = "SELECT has_schema_privilege('public', 'public', 'CREATE')"
		var publicCreate bool
		if err := p.Conn.QueryRow(ctx, publicCreateSQL).Scan(&publicCreate); err != nil {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Unknown,
				Section: section,
				Title:   "Public-schema privilege check unavailable",
				Message: fmt.Sprintf("Could not query has_schema_privilege for public schema CREATE privilege: %v", err),
			})
		} else if publicCreate {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Warn,
				Section: section,
				Title:   "PUBLIC retains CREATE on schema public",
				Message: "The PUBLIC pseudo-role still has CREATE privilege on the public schema. Since PostgreSQL 15 this is revoked by default; retaining it means any authenticated user can create objects in the public schema.",
				Detail:  "Run: REVOKE CREATE ON SCHEMA public FROM PUBLIC;",
				DocRef:  docDDLSchemas,
			})
		}
	}

	// --- allow_alter_system (PG≥17, setting) ------------------------------------
	// allow_alter_system=on (the default) lets any superuser run ALTER SYSTEM,
	// which writes postgresql.auto.conf and can override any server setting.
	// Setting it to off hardens against a compromised superuser making persistent
	// configuration changes.
	// src: https://www.postgresql.org/docs/17/runtime-config-client.html
	if p.Version.AtLeast(17, 0) {
		if aas, ok := p.Settings.Get("allow_alter_system"); ok && aas == "on" {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Info,
				Section: section,
				Title:   "allow_alter_system is on (default); consider disabling to harden (PG17+)",
				Message: "allow_alter_system=on. Any superuser can run ALTER SYSTEM, writing persistent overrides to postgresql.auto.conf. Setting this to off prevents accidental or malicious persistent configuration changes.",
				Detail:  "Set allow_alter_system=off in postgresql.conf to prevent ALTER SYSTEM. Changes must then be made via filesystem access to postgresql.conf directly.",
				DocRef:  docAlterSystem17,
			})
		}
	}

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.OK,
			Section: section,
			Title:   "Security configuration looks healthy",
			Message: "No high-priority security concerns detected: superuser count is minimal, password encryption is strong, and the public schema CREATE privilege has been revoked.",
			DocRef:  docRoleAttributes,
		})
	}
	return out
}
