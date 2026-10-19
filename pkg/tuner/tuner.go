// Package tuner runs a full tuning analysis and returns a Report.
package tuner

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/soylic/pgtuner/internal/checks"
	"github.com/soylic/pgtuner/internal/osinspect"
	"github.com/soylic/pgtuner/internal/pg"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// Analyze connects to PostgreSQL, runs all checks, and returns a Report.
func Analyze(ctx context.Context, o pgtuner.Options) (*pgtuner.Report, error) {
	conn, err := pg.Connect(ctx, pg.Options{DSN: o.DSN})
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

	baseProbe := &checks.Probe{
		Conn:     conn,
		Settings: settings,
		Version:  conn.Version,
		OS:       inspector,
		Now:      o.Now,
		Role:     role,
		Topology: topology,
		Flavor:   flavor,
	}

	if !o.AllDatabases {
		advice := checks.Default().RunAll(ctx, baseProbe)
		return withContext(pgtuner.NewReport(conn.Version.String(), advice), topology, flavor), nil
	}

	// Cluster-wide checks run once. Per-database checks run on each user database over its own connection.
	advice := checks.ClusterWide().RunAll(ctx, baseProbe)

	dbNames, err := pg.ListUserDatabases(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}

	perDB := checks.PerDatabase()
	for _, db := range dbNames {
		dbAdvice, err := analyzeDatabase(ctx, o.DSN, db, inspector, o.Now, role, perDB)
		if err != nil {
			// Report an Unknown so the other databases still get analyzed.
			advice = append(advice, dbFailureAdvice(db, err))
			continue
		}
		advice = append(advice, dbAdvice...)
	}

	rep := withContext(pgtuner.NewReport(conn.Version.String(), advice), topology, flavor)
	rep.Databases = dbNames
	return rep, nil
}

// withContext attaches the topology and flavor to the report.
func withContext(r *pgtuner.Report, t pgtuner.Topology, f pgtuner.Flavor) *pgtuner.Report {
	r.Topology = &t
	r.Flavor = &f
	return r
}

// dbFailureAdvice builds the Unknown advice for a database that failed to connect or load settings.
func dbFailureAdvice(db string, err error) pgtuner.Advice {
	return pgtuner.Advice{
		Level:    pgtuner.Unknown,
		Section:  "Per-Database",
		Title:    "Database analysis failed",
		Message:  fmt.Sprintf("Cannot analyze database %q: %v", db, err),
		Database: db,
	}
}

// analyzeDatabase runs the per-database registry on one database and tags the advice with its name.
func analyzeDatabase(ctx context.Context, dsn, db string, inspector osinspect.Inspector, now time.Time, role pg.Role, perDB checks.Registry) ([]pgtuner.Advice, error) {
	dbConn, err := pg.ConnectDB(ctx, dsn, db)
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
