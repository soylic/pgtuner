package pg

import "context"

// ServerRole describes the replication role of a PostgreSQL server.
// src: https://www.postgresql.org/docs/current/functions-admin.html (pg_is_in_recovery)
// src: https://www.postgresql.org/docs/current/monitoring-stats.html (pg_stat_replication)
type ServerRole string

const (
	// RolePrimary means the server is not in recovery and has at least one
	// connected streaming standby in pg_stat_replication.
	RolePrimary ServerRole = "primary"
	// RoleStandby means pg_is_in_recovery() returned true.
	RoleStandby ServerRole = "standby"
	// RoleStandalone means the server is not in recovery and has no connected
	// streaming standbys.
	RoleStandalone ServerRole = "standalone"
	// RoleUnknown means the role could not be determined (query error).
	RoleUnknown ServerRole = "unknown"
)

// Role describes the server's replication role at probe time.
type Role struct {
	Role              ServerRole
	InRecovery        bool
	ConnectedStandbys int
}

// DetectRole runs pg_is_in_recovery() and counts rows in pg_stat_replication.
// A server in recovery is a Standby; otherwise Primary if it has connected
// standbys, else Standalone. On any query error it returns Role{Role: RoleUnknown}
// and the error — callers should treat this as best-effort and continue.
// src: https://www.postgresql.org/docs/current/functions-admin.html (pg_is_in_recovery)
// src: https://www.postgresql.org/docs/current/monitoring-stats.html (pg_stat_replication)
func DetectRole(ctx context.Context, c *Conn) (Role, error) {
	var inRecovery bool
	if err := c.QueryRow(ctx, "SELECT pg_is_in_recovery()").Scan(&inRecovery); err != nil {
		return Role{Role: RoleUnknown}, err
	}
	var standbys int
	if err := c.QueryRow(ctx, "SELECT count(*) FROM pg_stat_replication").Scan(&standbys); err != nil {
		return Role{Role: RoleUnknown}, err
	}
	r := Role{InRecovery: inRecovery, ConnectedStandbys: standbys}
	switch {
	case inRecovery:
		r.Role = RoleStandby
	case standbys > 0:
		r.Role = RolePrimary
	default:
		r.Role = RoleStandalone
	}
	return r, nil
}
