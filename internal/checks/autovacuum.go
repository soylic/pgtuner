package checks

import (
	"context"
	"fmt"
	"strings"

	"github.com/soylic/pgtuner/pkg/pgtuner"
)

const docAutovacuumRuntime = "https://www.postgresql.org/docs/current/runtime-config-autovacuum.html"

const docVacuumWraparound = "https://www.postgresql.org/docs/current/routine-vacuuming.html#VACUUM-FOR-WRAPAROUND"

const docPGStatUserTables = "https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-TABLES-VIEW"

// deadTupleQueryLimit caps the query so only the worst tables are examined.
const deadTupleQueryLimit = 10

// deadTupleMessageCap is the maximum number of table names in the advice message.
const deadTupleMessageCap = 5

// DeadTupleRow holds one row from pg_stat_user_tables for dead-tuple analysis.
type DeadTupleRow struct {
	SchemaName string
	RelName    string
	DeadTuples int64
	LiveTuples int64
}

// evalDeadTuples flags tables where n_dead_tup > baseThreshold + scaleFactor * n_live_tup,
// the autovacuum trigger formula.
// src: https://www.postgresql.org/docs/current/routine-vacuuming.html#AUTOVACUUM
// src: https://www.postgresql.org/docs/current/runtime-config-autovacuum.html#GUC-AUTOVACUUM-VACUUM-THRESHOLD
func evalDeadTuples(rows []DeadTupleRow, baseThreshold float64, scaleFactor float64) []pgtuner.Advice {
	const section = "Autovacuum"

	var offenders []string
	for _, r := range rows {
		trigger := baseThreshold + scaleFactor*float64(r.LiveTuples)
		if float64(r.DeadTuples) > trigger {
			offenders = append(offenders, fmt.Sprintf("%s.%s (%d dead)", r.SchemaName, r.RelName, r.DeadTuples))
		}
	}

	if len(offenders) == 0 {
		return nil
	}

	capped := offenders
	suffix := ""
	if len(offenders) > deadTupleMessageCap {
		capped = offenders[:deadTupleMessageCap]
		suffix = fmt.Sprintf(" … and %d more", len(offenders)-deadTupleMessageCap)
	}

	return []pgtuner.Advice{{
		ID:      "autovacuum.dead-tuples-overdue",
		Level:   pgtuner.Warn,
		Section: section,
		Title:   "Tables with excessive dead tuples (vacuum overdue)",
		Message: fmt.Sprintf("%d table(s) exceed the autovacuum dead-tuple threshold (threshold=%g + %.2f × live_tup): %s%s. Autovacuum should reclaim these. If it has not, check for long-running transactions or autovacuum backlog.",
			len(offenders), baseThreshold, scaleFactor, strings.Join(capped, ", "), suffix),
		Detail: "Examine pg_stat_activity for transactions that may block autovacuum. You can also run VACUUM manually on the listed tables.",
		DocRef: docPGStatUserTables,
	}}
}

// evalWraparound compares the database age against autovacuum_freeze_max_age.
// src: https://www.postgresql.org/docs/current/routine-vacuuming.html#VACUUM-FOR-WRAPAROUND
func evalWraparound(age, maxAge int64) *pgtuner.Advice {
	const section = "Autovacuum"

	if maxAge <= 0 {
		// A zero or negative maxAge means the setting failed to parse.
		return nil
	}

	pct := float64(age) / float64(maxAge)

	switch {
	case pct > 0.80:
		a := pgtuner.Advice{
			ID:      "autovacuum.wraparound-critical",
			Level:   pgtuner.Bad,
			Section: section,
			Title:   "Transaction-ID wraparound risk is critical",
			Message: fmt.Sprintf("Database age is %d transactions (%.1f%% of autovacuum_freeze_max_age=%d). PostgreSQL will force an emergency autovacuum shutdown to prevent wraparound. Run VACUUM FREEZE on the database immediately.", age, pct*100, maxAge),
			Detail:  "Run VACUUM FREEZE on all affected databases. At 100%, PostgreSQL will refuse new transactions.",
			DocRef:  docVacuumWraparound,
		}
		return &a
	case pct > 0.50:
		a := pgtuner.Advice{
			ID:      "autovacuum.wraparound-elevated",
			Level:   pgtuner.Warn,
			Section: section,
			Title:   "Transaction-ID wraparound age is elevated",
			Message: fmt.Sprintf("Database age is %d transactions (%.1f%% of autovacuum_freeze_max_age=%d). Plan a VACUUM FREEZE before reaching 80%%.", age, pct*100, maxAge),
			Detail:  "Monitor pg_database.age(datfrozenxid) regularly. Make sure that autovacuum can run anti-wraparound vacuums.",
			DocRef:  docVacuumWraparound,
		}
		return &a
	default:
		return nil
	}
}

// evalWorkersCPU compares autovacuum_max_workers against the CPU budget.
// It returns nil when workers ≤ cpus.
// src: https://www.postgresql.org/docs/current/runtime-config-autovacuum.html#GUC-AUTOVACUUM-MAX-WORKERS
func evalWorkersCPU(section string, workers int, cpus float64) []pgtuner.Advice {
	if float64(workers) <= cpus {
		return nil
	}
	return []pgtuner.Advice{{
		ID:      "autovacuum.max-workers-exceeds-cpus",
		Level:   pgtuner.Info,
		Section: section,
		Title:   "autovacuum_max_workers exceeds available CPUs",
		Message: fmt.Sprintf("autovacuum_max_workers=%d exceeds available CPUs (%.1f). If all workers run concurrently, they will contend for CPU time. This slows both autovacuum and foreground queries.", workers, cpus),
		Detail:  "Consider setting autovacuum_max_workers to at most the number of available CPUs. Or reduce autovacuum_cost_delay to spread I/O and CPU load.",
		DocRef:  docAutovacuumRuntime,
	}}
}

// AutovacuumSettingsCheck evaluates cluster-wide autovacuum configuration GUCs:
// autovacuum on/off, autovacuum_max_workers, autovacuum_worker_slots (PG18+),
// vacuum_failsafe_age (PG14+), and the workers-vs-CPU budget. Under
// -all-databases they are reported once, not per database.
type AutovacuumSettingsCheck struct{ clusterScoped }

func (AutovacuumSettingsCheck) Name() string { return "autovacuum_settings" }

// Run emits an OK when no setting fires. It never panics and never returns an error.
func (AutovacuumSettingsCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "Autovacuum"
	var out []pgtuner.Advice

	// autovacuum=off leaves dead tuples and wraparound risk unchecked.
	// src: https://www.postgresql.org/docs/current/runtime-config-autovacuum.html#GUC-AUTOVACUUM
	if av, ok := p.Settings.Get("autovacuum"); ok && av == "off" {
		out = append(out, pgtuner.Advice{
			ID:      "autovacuum_settings.autovacuum-disabled",
			Level:   pgtuner.Bad,
			Section: section,
			Title:   "autovacuum is disabled: bloat and wraparound risk",
			Message: "autovacuum=off. Without automatic vacuuming, dead tuples accumulate. This causes table bloat, query slowdowns, and eventually transaction-ID wraparound, a forced shutdown.",
			Detail:  "Re-enable autovacuum=on. It is safe to disable it only for short periods during bulk-load operations.",
			DocRef:  docAutovacuumRuntime,
		})
	}

	// Flag only below the default of 3. Flagging 3 is too noisy without a busy-ness signal.
	// src: https://www.postgresql.org/docs/current/runtime-config-autovacuum.html#GUC-AUTOVACUUM-MAX-WORKERS
	if mw, ok := p.Settings.Int("autovacuum_max_workers"); ok && mw < 3 {
		out = append(out, pgtuner.Advice{
			ID:      "autovacuum_settings.max-workers-below-default",
			Level:   pgtuner.Info,
			Section: section,
			Title:   "autovacuum_max_workers is below the default",
			Message: fmt.Sprintf("autovacuum_max_workers=%d is below the default of 3. On databases with many tables, too few workers can leave tables unvacuumed for extended periods.", mw),
			Detail:  "Raise autovacuum_max_workers to at least 3, or higher on systems with many busy tables.",
			DocRef:  docAutovacuumRuntime,
		})
	}

	// autovacuum_worker_slots was introduced in PG18.
	// src: https://www.postgresql.org/docs/18/runtime-config-autovacuum.html
	if p.Version.AtLeast(18, 0) {
		if _, ok := p.Settings.Get("autovacuum_worker_slots"); ok {
			out = append(out, pgtuner.Advice{
				ID:      "autovacuum_settings.worker-slots-pg18",
				Level:   pgtuner.Info,
				Section: section,
				Title:   "autovacuum_worker_slots available (PG18+)",
				Message: "PostgreSQL 18+ adds autovacuum_worker_slots. This setting limits how many autovacuum workers can be active at the same time. Review its value together with autovacuum_max_workers.",
				DocRef:  "https://www.postgresql.org/docs/18/runtime-config-autovacuum.html",
			})
		}
	}

	// PG14+: above vacuum_failsafe_age, VACUUM skips index vacuuming to finish faster.
	// The default of 1.6 billion is well above autovacuum_freeze_max_age.
	// src: https://www.postgresql.org/docs/14/runtime-config-client.html#GUC-VACUUM-FAILSAFE-AGE
	if p.Version.AtLeast(14, 0) {
		if vfa, ok := p.Settings.Int("vacuum_failsafe_age"); ok {
			fma, _ := p.Settings.Int("autovacuum_freeze_max_age")
			if fma > 0 && vfa < fma {
				out = append(out, pgtuner.Advice{
					ID:      "autovacuum_settings.failsafe-age-below-freeze-max-age",
					Level:   pgtuner.Info,
					Section: section,
					Title:   "vacuum_failsafe_age is below autovacuum_freeze_max_age",
					Message: fmt.Sprintf("vacuum_failsafe_age=%d is below autovacuum_freeze_max_age=%d. Failsafe vacuum will start before normal anti-wraparound vacuum. This is unusual.", vfa, fma),
					DocRef:  "https://www.postgresql.org/docs/14/runtime-config-client.html#GUC-VACUUM-FAILSAFE-AGE",
				})
			}
		}
	}

	// On a cgroup-constrained host the CPU quota may be well below the physical count.
	// src: https://www.postgresql.org/docs/current/runtime-config-autovacuum.html#GUC-AUTOVACUUM-MAX-WORKERS
	// src: https://docs.kernel.org/admin-guide/cgroup-v2.html (cpu.max)
	if mwCPU, okMW := p.Settings.Int("autovacuum_max_workers"); okMW {
		if effCPUs, _, cpuErr := p.OS.EffectiveCPUs(); cpuErr == nil {
			out = append(out, evalWorkersCPU(section, int(mwCPU), effCPUs)...)
		}
		// Skipped when OS facts are unavailable.
	}

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			ID:      "autovacuum_settings.settings-ok",
			Level:   pgtuner.OK,
			Section: section,
			Title:   "Autovacuum settings look healthy",
			Message: "autovacuum is enabled and the worker configuration is within sensible limits.",
			DocRef:  docAutovacuumRuntime,
		})
	}
	return out
}

// AutovacuumCheck evaluates dead-tuple accumulation and transaction-ID wraparound
// risk from per-database stats, so under -all-databases it runs once per user database.
type AutovacuumCheck struct{ databaseScoped }

func (AutovacuumCheck) Name() string { return "autovacuum" }

// Run executes the dead-tuple and wraparound sub-checks independently. A query
// failure in one yields a single Unknown advice and does not stop the other.
// It never panics and never returns an error.
func (AutovacuumCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "Autovacuum"
	var out []pgtuner.Advice

	// Read thresholds from the live settings so they match what autovacuum uses.
	// src: https://www.postgresql.org/docs/current/runtime-config-autovacuum.html#GUC-AUTOVACUUM-VACUUM-THRESHOLD
	// src: https://www.postgresql.org/docs/current/runtime-config-autovacuum.html#GUC-AUTOVACUUM-VACUUM-SCALE-FACTOR
	baseThreshold, _ := p.Settings.Float("autovacuum_vacuum_threshold")
	if baseThreshold == 0 {
		baseThreshold = 50 // documented default
	}
	scaleFactor, _ := p.Settings.Float("autovacuum_vacuum_scale_factor")
	if scaleFactor == 0 {
		scaleFactor = 0.2 // documented default
	}

	// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-TABLES-VIEW
	const deadSQL = `SELECT schemaname, relname, n_dead_tup, n_live_tup
		FROM pg_stat_user_tables
		WHERE n_dead_tup > 0
		ORDER BY n_dead_tup DESC
		LIMIT $1`

	deadRows, err := p.Conn.Query(ctx, deadSQL, deadTupleQueryLimit)
	if err != nil {
		out = append(out, pgtuner.Advice{
			ID:      "autovacuum.dead-tuples-query-failed",
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Dead-tuple stats unavailable",
			Message: fmt.Sprintf("Could not query pg_stat_user_tables for dead-tuple statistics: %v", err),
		})
	} else {
		func() {
			defer deadRows.Close()
			var rows []DeadTupleRow
			for deadRows.Next() {
				var r DeadTupleRow
				if err := deadRows.Scan(&r.SchemaName, &r.RelName, &r.DeadTuples, &r.LiveTuples); err != nil {
					out = append(out, pgtuner.Advice{
						ID:      "autovacuum.dead-tuples-scan-failed",
						Level:   pgtuner.Unknown,
						Section: section,
						Title:   "Dead-tuple stats scan failed",
						Message: fmt.Sprintf("Could not scan pg_stat_user_tables row: %v", err),
					})
					return
				}
				rows = append(rows, r)
			}
			if err := deadRows.Err(); err != nil {
				out = append(out, pgtuner.Advice{
					ID:      "autovacuum.dead-tuples-iteration-failed",
					Level:   pgtuner.Unknown,
					Section: section,
					Title:   "Dead-tuple stats iteration failed",
					Message: fmt.Sprintf("Error iterating pg_stat_user_tables rows: %v", err),
				})
				return
			}
			out = append(out, evalDeadTuples(rows, baseThreshold, scaleFactor)...)
		}()
	}

	// age(datfrozenxid) counts transactions since the last freeze. autovacuum forces
	// a freeze vacuum at autovacuum_freeze_max_age (default 200,000,000).
	// src: https://www.postgresql.org/docs/current/routine-vacuuming.html#VACUUM-FOR-WRAPAROUND
	maxAge, _ := p.Settings.Int("autovacuum_freeze_max_age")
	if maxAge == 0 {
		maxAge = 200_000_000 // documented default
	}

	const wraparoundSQL = "SELECT datname, age(datfrozenxid) FROM pg_database WHERE datname = current_database()"
	var datname string
	var xidAge int64
	if err := p.Conn.QueryRow(ctx, wraparoundSQL).Scan(&datname, &xidAge); err != nil {
		out = append(out, pgtuner.Advice{
			ID:      "autovacuum.wraparound-query-failed",
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Wraparound age unavailable",
			Message: fmt.Sprintf("Could not query pg_database for transaction-ID age: %v", err),
		})
	} else if a := evalWraparound(xidAge, maxAge); a != nil {
		out = append(out, *a)
	}

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			ID:      "autovacuum.keeping-up-ok",
			Level:   pgtuner.OK,
			Section: section,
			Title:   "Autovacuum is keeping up",
			Message: "No tables have excessive dead tuples and the transaction-ID age is well within safe limits for this database.",
			DocRef:  docAutovacuumRuntime,
		})
	}
	return out
}
