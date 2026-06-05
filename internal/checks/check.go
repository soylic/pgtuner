package checks

import (
	"context"
	"time"

	"github.com/OpenRemedy/pgtuner/internal/osinspect"
	"github.com/OpenRemedy/pgtuner/internal/pg"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// Probe is everything a Check needs: an open connection, the cached settings
// snapshot, the parsed server version, an OS inspector (never nil; a
// NoopInspector is used when no OS facts are available), and the detected
// replication role (populated once in Analyze; Role.Role == RoleUnknown when
// detection failed).
type Probe struct {
	Conn     *pg.Conn
	Settings *pg.Settings
	Version  pg.ServerVersion
	OS       osinspect.Inspector
	Now      time.Time  // when zero, checks use time.Now()
	Role     pg.Role    // replication role; best-effort, never nil
}

// now returns the probe's fixed timestamp for deterministic testing, or the
// real wall clock when Now is zero.
func (p *Probe) now() time.Time {
	if p.Now.IsZero() {
		return time.Now()
	}
	return p.Now
}

// Scope describes whether a check examines cluster-wide state (settings, OS,
// replication topology) or state that is specific to a single database (its
// catalog, its stats rows in pg_stat_database / pg_stat_user_*). Under
// -all-databases, ClusterScope checks run once against the connection database
// while DatabaseScope checks run once per user database.
type Scope int

const (
	ClusterScope  Scope = iota // examines cluster-wide / instance-level state
	DatabaseScope              // examines state scoped to the current database
)

// clusterScoped is an embeddable helper that marks a check ClusterScope.
type clusterScoped struct{}

func (clusterScoped) Scope() Scope { return ClusterScope }

// databaseScoped is an embeddable helper that marks a check DatabaseScope.
type databaseScoped struct{}

func (databaseScoped) Scope() Scope { return DatabaseScope }

// Check is a single tuning check. Run must be side-effect free apart from
// reading from the Probe, and must never panic; on failure it returns an
// Unknown-level Advice rather than erroring. Scope reports whether the check is
// cluster-wide or per-database (see Scope); embed clusterScoped or
// databaseScoped to supply it.
type Check interface {
	Name() string
	Scope() Scope
	Run(ctx context.Context, p *Probe) []pgtuner.Advice
}

// Registry is an ordered list of checks.
type Registry []Check

// RunAll runs every check in order and concatenates their advice.
func (r Registry) RunAll(ctx context.Context, p *Probe) []pgtuner.Advice {
	var out []pgtuner.Advice
	for _, c := range r {
		out = append(out, c.Run(ctx, p)...)
	}
	return out
}
