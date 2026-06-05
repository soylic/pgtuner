// Package tuner orchestrates a full tuning analysis: connect, snapshot
// settings, run all checks, and assemble a Report.
package tuner

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/OpenRemedy/pgtuner/internal/checks"
	"github.com/OpenRemedy/pgtuner/internal/osinspect"
	"github.com/OpenRemedy/pgtuner/internal/pg"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
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

	// Detect replication role (best-effort; on error keep RoleUnknown and continue).
	// src: https://www.postgresql.org/docs/current/functions-admin.html (pg_is_in_recovery)
	// src: https://www.postgresql.org/docs/current/monitoring-stats.html (pg_stat_replication)
	role, _ := pg.DetectRole(ctx, conn)

	baseProbe := &checks.Probe{
		Conn:     conn,
		Settings: settings,
		Version:  conn.Version,
		OS:       inspector,
		Now:      o.Now,
		Role:     role,
	}

	if !o.AllDatabases {
		advice := checks.Default().RunAll(ctx, baseProbe)
		return pgtuner.NewReport(conn.Version.String(), advice), nil
	}

	// -all-databases: cluster-wide checks run once against the connection
	// database; per-database checks run once per user database, each over its
	// own connection, with their advice tagged with the database name.
	advice := checks.ClusterWide().RunAll(ctx, baseProbe)

	dbNames, err := pg.ListUserDatabases(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}

	perDB := checks.PerDatabase()
	for _, db := range dbNames {
		dbAdvice, err := analyzeDatabase(ctx, o.DSN, db, inspector, o.Now, role, perDB)
		if err != nil {
			// Surface the failure as an Unknown rather than aborting the whole
			// run; other databases still get analyzed.
			advice = append(advice, dbFailureAdvice(db, err))
			continue
		}
		advice = append(advice, dbAdvice...)
	}

	rep := pgtuner.NewReport(conn.Version.String(), advice)
	rep.Databases = dbNames
	return rep, nil
}

// dbFailureAdvice builds the tagged Unknown advice emitted when a per-database
// analysis fails to connect or load its settings. Surfacing the failure as an
// Unknown (rather than erroring) lets the remaining databases still be analyzed
// and keeps the failed database visible and attributed in the report.
func dbFailureAdvice(db string, err error) pgtuner.Advice {
	return pgtuner.Advice{
		Level:    pgtuner.Unknown,
		Section:  "Per-Database",
		Title:    "Database analysis failed",
		Message:  fmt.Sprintf("Could not analyze database %q: %v", db, err),
		Database: db,
	}
}

// analyzeDatabase connects to a single database and runs the per-database
// registry against it, tagging every resulting advice with the database name.
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
