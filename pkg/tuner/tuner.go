// Package tuner runs a full tuning analysis and returns a Report.
package tuner

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/soylic/pgtuner/internal/checks"
	"github.com/soylic/pgtuner/internal/osinspect"
	"github.com/soylic/pgtuner/internal/patroni"
	"github.com/soylic/pgtuner/internal/pg"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// pgOptions maps the public options to connection options.
func pgOptions(o pgtuner.Options) pg.Options {
	return pg.Options{DSN: o.DSN, StatementTimeout: o.StatementTimeout}
}

// Analyze connects to PostgreSQL, runs the checks, and returns a Report.
func Analyze(ctx context.Context, o pgtuner.Options) (*pgtuner.Report, error) {
	for _, p := range o.Ignore {
		if err := pgtuner.ValidateIDPattern(p); err != nil {
			return nil, err
		}
	}
	// Validate the check names before any connection, so a typo fails fast.
	all, err := checks.Default().Select(o.Only, o.Skip)
	if err != nil {
		return nil, err
	}

	opts := pgOptions(o)
	conn, err := pg.Connect(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	settings, err := pg.LoadSettings(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}

	var inspector osinspect.Inspector = osinspect.NoopInspector{}
	if o.OSInspect && runtime.GOOS == "linux" {
		inspector = osinspect.LocalInspector{}
	}

	// Best effort: on error the role stays RoleUnknown.
	// src: https://www.postgresql.org/docs/current/functions-admin.html (pg_is_in_recovery)
	// src: https://www.postgresql.org/docs/current/monitoring-stats.html (pg_stat_replication)
	role, _ := pg.DetectRole(ctx, conn)
	topology := pg.DetectTopology(ctx, conn, role, settings)
	flavor := pg.DetectFlavor(ctx, conn, settings)
	patroniErr := ""
	if o.PatroniURL != "" {
		patroniErr = applyPatroni(ctx, &topology, o.PatroniURL)
	}

	baseProbe := &checks.Probe{
		Conn:         conn,
		Settings:     settings,
		Version:      conn.Version,
		OS:           inspector,
		Now:          o.Now,
		Role:         role,
		Topology:     topology,
		Flavor:       flavor,
		PatroniError: patroniErr,
	}

	var advice []pgtuner.Advice
	var dbNames []string
	if !o.AllDatabases {
		advice = all.RunAll(ctx, baseProbe)
	} else {
		// Cluster-wide checks run once. Per-database checks run on each user database over its own connection.
		cluster := checks.ClusterWide().Filter(o.Only, o.Skip)
		perDB := checks.PerDatabase().Filter(o.Only, o.Skip)
		advice = cluster.RunAll(ctx, baseProbe)

		dbNames, err = pg.ListUserDatabases(ctx, conn)
		if err != nil {
			return nil, fmt.Errorf("list databases: %w", err)
		}
		for _, db := range dbNames {
			dbAdvice, err := analyzeDatabase(ctx, opts, db, inspector, o.Now, role, perDB)
			if err != nil {
				// Report an Unknown so the other databases still get analyzed.
				advice = append(advice, dbFailureAdvice(db, err))
				continue
			}
			advice = append(advice, dbAdvice...)
		}
	}

	advice = checks.AdaptToEnvironment(advice, settings, topology, flavor)
	advice, ignored := pgtuner.FilterIgnored(advice, o.Ignore)

	rep := pgtuner.NewReport(conn.Version.String(), advice)
	rep.Topology = &topology
	rep.Flavor = &flavor
	rep.Ignored = ignored
	rep.Databases = dbNames
	return rep, nil
}

// applyPatroni asks the Patroni REST API for the member list. On success it
// makes the Patroni match certain and returns "". On failure it returns the
// reason and leaves the topology as the SQL probes found it.
func applyPatroni(ctx context.Context, t *pgtuner.Topology, rawURL string) string {
	base, err := patroni.BaseURL(rawURL)
	if err != nil {
		return err.Error()
	}
	cl, err := patroni.Fetch(ctx, base)
	if err != nil {
		return err.Error()
	}
	evidence := []string{fmt.Sprintf("the REST API at %s lists cluster %q", base, cl.Scope)}
	if t.Manager != nil {
		evidence = append(evidence, t.Manager.Evidence...)
	}
	t.Manager = &pgtuner.Manager{Name: "patroni", Confidence: "certain", Evidence: evidence, Paused: cl.Paused}
	t.Members = nil
	for _, m := range cl.Members {
		mem := pgtuner.Member{
			Name: m.Name, Host: m.Host, Port: m.Port, Role: m.Role,
			State: m.State, Timeline: m.Timeline,
			Active: m.State == "running" || m.State == "streaming",
		}
		if m.LagBytes >= 0 {
			lag := m.LagBytes
			mem.LagBytes = &lag
		}
		t.Members = append(t.Members, mem)
	}
	return ""
}

// dbFailureAdvice builds the Unknown advice for a database that failed to connect or load settings.
func dbFailureAdvice(db string, err error) pgtuner.Advice {
	return pgtuner.Advice{
		ID:       "analysis.database-failed",
		Level:    pgtuner.Unknown,
		Section:  "Per-Database",
		Title:    "Database analysis failed",
		Message:  fmt.Sprintf("Cannot analyze database %q: %v", db, err),
		Database: db,
	}
}

// analyzeDatabase runs the per-database registry on one database and tags the advice with its name.
func analyzeDatabase(ctx context.Context, opts pg.Options, db string, inspector osinspect.Inspector, now time.Time, role pg.Role, perDB checks.Registry) ([]pgtuner.Advice, error) {
	dbConn, err := pg.ConnectDB(ctx, opts, db)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer dbConn.Close(ctx)

	dbSettings, err := pg.LoadSettings(ctx, dbConn)
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}

	probe := &checks.Probe{
		Conn:     dbConn,
		Settings: dbSettings,
		Version:  dbConn.Version,
		OS:       inspector,
		Now:      now,
		Role:     role,
	}
	advice := perDB.RunAll(ctx, probe)
	for i := range advice {
		advice[i].Database = db
	}
	return advice, nil
}
