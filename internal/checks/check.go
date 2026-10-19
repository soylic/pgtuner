package checks

import (
	"context"
	"time"

	"github.com/soylic/pgtuner/internal/osinspect"
	"github.com/soylic/pgtuner/internal/pg"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// Probe is the input a Check needs. OS is never nil (a NoopInspector stands in
// when no OS facts exist). Role.Role is RoleUnknown when detection failed.
type Probe struct {
	Conn     *pg.Conn
	Settings *pg.Settings
	Version  pg.ServerVersion
	OS       osinspect.Inspector
	Now      time.Time // when zero, checks use time.Now()
	Role     pg.Role   // replication role; best-effort, never nil
	Topology pgtuner.Topology
	Flavor   pgtuner.Flavor
	// PatroniError is set when -patroni-url was given and the API did not answer as Patroni.
	PatroniError string
}

// now returns the fixed Now for deterministic tests, or the wall clock when zero.
func (p *Probe) now() time.Time {
	if p.Now.IsZero() {
		return time.Now()
	}
	return p.Now
}

// Scope says whether a check reads cluster-wide state or per-database state.
// Under -all-databases, ClusterScope checks run once and DatabaseScope checks
// run once per user database.
type Scope int

const (
	ClusterScope Scope = iota
	DatabaseScope
)

type clusterScoped struct{}

func (clusterScoped) Scope() Scope { return ClusterScope }

type databaseScoped struct{}

func (databaseScoped) Scope() Scope { return DatabaseScope }

// Check is a single tuning check. Run must not panic. On failure it returns an
// Unknown-level Advice instead of an error.
type Check interface {
	Name() string
	Scope() Scope
	Run(ctx context.Context, p *Probe) []pgtuner.Advice
}

type Registry []Check

func (r Registry) RunAll(ctx context.Context, p *Probe) []pgtuner.Advice {
	var out []pgtuner.Advice
	for _, c := range r {
		out = append(out, c.Run(ctx, p)...)
	}
	return out
}
