package checks

import (
	"context"
	"fmt"
	"strings"

	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// docAutovacuumRuntime is the canonical reference for autovacuum GUCs.
// src: https://www.postgresql.org/docs/current/runtime-config-autovacuum.html
const docAutovacuumRuntime = "https://www.postgresql.org/docs/current/runtime-config-autovacuum.html"

// docVacuumWraparound is the canonical reference for transaction-ID wraparound.
// src: https://www.postgresql.org/docs/current/routine-vacuuming.html#VACUUM-FOR-WRAPAROUND
const docVacuumWraparound = "https://www.postgresql.org/docs/current/routine-vacuuming.html#VACUUM-FOR-WRAPAROUND"

// docPGStatUserTables is the canonical reference for pg_stat_user_tables columns.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-TABLES-VIEW
const docPGStatUserTables = "https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ALL-TABLES-VIEW"

// deadTupleQueryLimit is the number of top-offending tables to fetch from
// pg_stat_user_tables. We cap the query itself so we only examine the worst few.
const deadTupleQueryLimit = 10

// deadTupleMessageCap is the maximum number of table names to include in the
// advice message, to keep it readable.
const deadTupleMessageCap = 5

// DeadTupleRow holds one row from pg_stat_user_tables for dead-tuple analysis.
type DeadTupleRow struct {
	SchemaName string
	RelName    string
	DeadTuples int64
	LiveTuples int64
}

// evalDeadTuples is the pure decision function for dead-tuple vacuum advice.
// A table needs vacuum when n_dead_tup > baseThreshold + scaleFactor * n_live_tup,
// mirroring the autovacuum trigger formula from the PostgreSQL documentation.
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
		Level:   pgtuner.Warn,
		Section: section,
		Title:   "Tables with excessive dead tuples (vacuum overdue)",
		Message: fmt.Sprintf("%d table(s) exceed the autovacuum dead-tuple threshold (threshold=%g + %.2f × live_tup): %s%s. Autovacuum should reclaim these; if it has not, check for long-running transactions or autovacuum backlog.",
			len(offenders), baseThreshold, scaleFactor, strings.Join(capped, ", "), suffix),
		Detail: "Examine pg_stat_activity for transactions that may be blocking autovacuum, or run VACUUM manually on the listed tables.",
		DocRef: docPGStatUserTables,
	}}
}

// evalWraparound is the pure decision function for transaction-ID wraparound advice.
// src: https://www.postgresql.org/docs/current/routine-vacuuming.html#VACUUM-FOR-WRAPAROUND
func evalWraparound(age, maxAge int64) *pgtuner.Advice {
	const section = "Autovacuum"

	if maxAge <= 0 {
		// Guard against a zero/negative maxAge from Settings parse failure; skip.
		return nil
	}

	pct := float64(age) / float64(maxAge)

	switch {
	case pct > 0.80:
		// src: https://www.postgresql.org/docs/current/routine-vacuuming.html#VACUUM-FOR-WRAPAROUND
		a := pgtuner.Advice{
			Level:   pgtuner.Bad,
			Section: section,
			Title:   "Transaction-ID wraparound risk is critical",
			Message: fmt.Sprintf("Database age is %d transactions (%.1f%% of autovacuum_freeze_max_age=%d). PostgreSQL will force an emergency autovacuum shutdown to prevent wraparound. Run VACUUM FREEZE on the database immediately.", age, pct*100, maxAge),
			Detail:  "Run VACUUM FREEZE on all affected databases. At 100% PostgreSQL will refuse new transactions.",
			DocRef:  docVacuumWraparound,
		}
		return &a
	case pct > 0.50:
		// src: https://www.postgresql.org/docs/current/routine-vacuuming.html#VACUUM-FOR-WRAPAROUND
		a := pgtuner.Advice{
			Level:   pgtuner.Warn,
			Section: section,
			Title:   "Transaction-ID wraparound age is elevated",
			Message: fmt.Sprintf("Database age is %d transactions (%.1f%% of autovacuum_freeze_max_age=%d). Plan a VACUUM FREEZE before reaching 80%%.", age, pct*100, maxAge),
			Detail:  "Monitor pg_database.age(datfrozenxid) regularly and ensure autovacuum can run anti-wraparound vacuums.",
			DocRef:  docVacuumWraparound,
		}
		return &a
	default:
		return nil
	}
}

// evalWorkersCPU is the pure decision function comparing autovacuum_max_workers
// against the available CPU budget. When workers > cpus each worker that fires
// at the same time competes for a CPU slice, increasing query latency.
// An empty slice means no issue (workers ≤ cpus).
// src: https://www.postgresql.org/docs/current/runtime-config-autovacuum.html#GUC-AUTOVACUUM-MAX-WORKERS
func evalWorkersCPU(section string, workers int, cpus float64) []pgtuner.Advice {
	if float64(workers) <= cpus {
		return nil
	}
	return []pgtuner.Advice{{
		Level:   pgtuner.Info,
		Section: section,
		Title:   "autovacuum_max_workers exceeds available CPUs",
		Message: fmt.Sprintf("autovacuum_max_workers=%d exceeds available CPUs (%.1f); if all workers run concurrently they will contend for CPU time, slowing both autovacuum and foreground queries.", workers, cpus),
		Detail:  "Consider setting autovacuum_max_workers to at most the number of available CPUs, or reduce autovacuum_cost_delay to spread I/O and CPU load.",
		DocRef:  docAutovacuumRuntime,
	}}
}

// AutovacuumSettingsCheck evaluates cluster-wide autovacuum configuration GUCs:
// autovacuum on/off, autovacuum_max_workers, autovacuum_worker_slots (PG18+),
// vacuum_failsafe_age (PG14+), and the workers-vs-CPU budget. These are
// instance-level settings, so under -all-databases they must be reported once,
// not repeated per database.
type AutovacuumSettingsCheck struct{ clusterScoped }

func (AutovacuumSettingsCheck) Name() string { return "autovacuum_settings" }

// Run evaluates cluster-wide autovacuum settings. When none fire it emits an OK.
// It never panics and never returns an error.
func (AutovacuumSettingsCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "Autovacuum"
	var out []pgtuner.Advice

	// --- autovacuum GUC -----------------------------------------------------------
	// autovacuum=off disables all automatic vacuuming; tables accumulate dead tuples
	// indefinitely and wraparound risk grows unchecked.
	// src: https://www.postgresql.org/docs/current/runtime-config-autovacuum.html#GUC-AUTOVACUUM
	if av, ok := p.Settings.Get("autovacuum"); ok && av == "off" {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Bad,
			Section: section,
			Title:   "autovacuum is disabled — bloat and wraparound risk",
			Message: "autovacuum=off. Without automatic vacuuming, dead tuples accumulate causing table bloat, query slowdowns, and eventually transaction-ID wraparound — a forced shutdown.",
			Detail:  "Re-enable autovacuum=on. It is only safe to disable for short periods during bulk-load operations.",
			DocRef:  docAutovacuumRuntime,
		})
	}

	// autovacuum_max_workers: only flag if unusually low (< 3, the default).
	// Alerting on == 3 (default) is too noisy; we have no busy-ness signal.
	// src: https://www.postgresql.org/docs/current/runtime-config-autovacuum.html#GUC-AUTOVACUUM-MAX-WORKERS
	if mw, ok := p.Settings.Int("autovacuum_max_workers"); ok && mw < 3 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "autovacuum_max_workers is below the default",
			Message: fmt.Sprintf("autovacuum_max_workers=%d is below the default of 3. On databases with many tables, too few workers can leave tables unvacuumed for extended periods.", mw),
			Detail:  "Raise autovacuum_max_workers to at least 3, or higher on systems with many busy tables.",
			DocRef:  docAutovacuumRuntime,
		})
	}

	// PG≥18: autovacuum_worker_slots — informational note about the new knob.
	// This GUC was introduced in PG18 alongside the parallel vacuum worker model.
	// src: https://www.postgresql.org/docs/18/runtime-config-autovacuum.html
	if p.Version.AtLeast(18, 0) {
		if _, ok := p.Settings.Get("autovacuum_worker_slots"); ok {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Info,
				Section: section,
				Title:   "autovacuum_worker_slots available (PG18+)",
				Message: "PostgreSQL 18+ adds autovacuum_worker_slots, which limits how many autovacuum workers can be active simultaneously. Review its value alongside autovacuum_max_workers.",
				DocRef:  "https://www.postgresql.org/docs/18/runtime-config-autovacuum.html",
			})
		}
	}

	// PG≥14: vacuum_failsafe_age — informational note about this safety valve.
	// When a relation's age exceeds vacuum_failsafe_age, VACUUM runs in a special
	// failsafe mode that skips index vacuuming to reduce WAL traffic and complete
	// faster. The default is 1.6 billion, well above autovacuum_freeze_max_age.
	// src: https://www.postgresql.org/docs/14/runtime-config-client.html#GUC-VACUUM-FAILSAFE-AGE
	if p.Version.AtLeast(14, 0) {
		if vfa, ok := p.Settings.Int("vacuum_failsafe_age"); ok {
			fma, _ := p.Settings.Int("autovacuum_freeze_max_age")
			if fma > 0 && vfa < fma {
				out = append(out, pgtuner.Advice{
					Level:   pgtuner.Info,
					Section: section,
					Title:   "vacuum_failsafe_age is below autovacuum_freeze_max_age",
					Message: fmt.Sprintf("vacuum_failsafe_age=%d is below autovacuum_freeze_max_age=%d. Failsafe vacuum will trigger before normal anti-wraparound vacuum, which is unusual.", vfa, fma),
					DocRef:  "https://www.postgresql.org/docs/14/runtime-config-client.html#GUC-VACUUM-FAILSAFE-AGE",
				})
			}
		}
	}

	// --- autovacuum_max_workers vs. effective CPUs --------------------------------
	// When autovacuum_max_workers exceeds the available CPU budget each worker
	// that wakes simultaneously must share time. On a cgroup-constrained host the
	// CPU quota may be well below the host's physical count.
	// src: https://www.postgresql.org/docs/current/runtime-config-autovacuum.html#GUC-AUTOVACUUM-MAX-WORKERS
	// src: https://docs.kernel.org/admin-guide/cgroup-v2.html (cpu.max)
	if mwCPU, okMW := p.Settings.Int("autovacuum_max_workers"); okMW {
		if effCPUs, _, cpuErr := p.OS.EffectiveCPUs(); cpuErr == nil {
			out = append(out, evalWorkersCPU(section, int(mwCPU), effCPUs)...)
		}
		// Degrade gracefully when ErrNoOSFacts: skip this sub-check.
	}

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.OK,
			Section: section,
			Title:   "Autovacuum settings look healthy",
			Message: "autovacuum is enabled and the worker configuration is within sensible limits.",
			DocRef:  docAutovacuumRuntime,
		})
	}
	return out
}

// AutovacuumCheck evaluates per-database autovacuum effectiveness:
// dead-tuple accumulation and transaction-ID wraparound risk. These read
// per-database stats (pg_stat_user_tables, pg_database.age), so under
// -all-databases the check runs once per user database.
type AutovacuumCheck struct{ databaseScoped }

func (AutovacuumCheck) Name() string { return "autovacuum" }

// Run queries live per-database stats for dead-tuple and wraparound risk. Each
// sub-check (dead-tuples, wraparound) is independent; a query failure in one
// produces a single Unknown advice for that sub-check and does not prevent the
// other from running. When neither fires it emits an OK.
// It never panics and never returns an error.
func (AutovacuumCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "Autovacuum"
	var out []pgtuner.Advice

	// --- dead tuples (stats query) ------------------------------------------------
	// Read GUC defaults from the live settings snapshot so advice thresholds match
	// what autovacuum is actually using.
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

	// --- wraparound (stats query) -------------------------------------------------
	// age(datfrozenxid) returns how many transactions have occurred since the
	// database was last frozen. autovacuum_freeze_max_age (default 200,000,000)
	// is the threshold at which autovacuum will force a freeze vacuum.
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
			Level:   pgtuner.OK,
			Section: section,
			Title:   "Autovacuum is keeping up",
			Message: "No tables have excessive dead tuples and the transaction-ID age is well within safe limits for this database.",
			DocRef:  docAutovacuumRuntime,
		})
	}
	return out
}

