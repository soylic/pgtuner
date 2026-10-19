package pg

import "context"

// ServerRole describes the replication role of a PostgreSQL server.
// src: https://www.postgresql.org/docs/current/functions-admin.html (pg_is_in_recovery)
// src: https://www.postgresql.org/docs/current/monitoring-stats.html (pg_stat_replication)
type ServerRole string

const (
	// RolePrimary: not in recovery, with at least one connected streaming standby.
	RolePrimary ServerRole = "primary"
	// RoleStandby: pg_is_in_recovery() returned true.
	RoleStandby ServerRole = "standby"
	// RoleStandalone: not in recovery, with no connected streaming standbys.
	RoleStandalone ServerRole = "standalone"
	// RoleUnknown: a query error prevented detection.
	RoleUnknown ServerRole = "unknown"
)

// Role describes the server's replication role at probe time.
type Role struct {
	Role              ServerRole
	InRecovery        bool
	ConnectedStandbys int
}

// DetectRole returns Standby in recovery, else Primary with connected standbys, else Standalone.
// On a query error it returns RoleUnknown and the error. Callers can continue.
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
