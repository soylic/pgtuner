package checks

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/soylic/pgtuner/pkg/pgtuner"
)

const docPGSettings = "https://www.postgresql.org/docs/current/view-pg-settings.html"

const docRuntimeResource = "https://www.postgresql.org/docs/current/runtime-config-resource.html"

const docRuntimeQueryParallel = "https://www.postgresql.org/docs/current/runtime-config-query.html#RUNTIME-CONFIG-QUERY-OTHER"

const docRuntimeLogging = "https://www.postgresql.org/docs/current/runtime-config-logging.html"

const docRuntimeClient = "https://www.postgresql.org/docs/current/runtime-config-client.html"

const docPGStatArchiver = "https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ARCHIVER-VIEW"

const docAlterExtension = "https://www.postgresql.org/docs/current/sql-alterextension.html"

const configSection = "Configuration"

// configListCap is the maximum number of names in a message before "… and N more".
const configListCap = 10

// configCapList joins up to configListCap names using the shared capList helper (schema.go).
func configCapList(names []string) string {
	capped, suffix := capList(names, configListCap)
	return strings.Join(capped, ", ") + suffix
}

// evalPendingRestart flags GUCs changed (e.g. via ALTER SYSTEM) that need a
// restart, so the running configuration differs from the configured one.
// src: https://www.postgresql.org/docs/current/view-pg-settings.html
func evalPendingRestart(names []string) []pgtuner.Advice {
	if len(names) == 0 {
		return nil
	}
	return []pgtuner.Advice{{
		ID:      "config.pending-restart",
		Level:   pgtuner.Warn,
		Section: configSection,
		Title:   fmt.Sprintf("Settings changed but pending restart (%d)", len(names)),
		Message: fmt.Sprintf(
			"%d setting(s) have a new value that has not taken effect, because the value requires a server restart "+
				"(pg_settings.pending_restart is true): %s. The running server uses the old values.",
			len(names), configCapList(names)),
		Detail: "Restart the PostgreSQL server during a maintenance window to apply these settings. " +
			"Verify with: SELECT name, setting, pending_restart FROM pg_settings WHERE pending_restart.",
		DocRef: docPGSettings,
	}}
}

// evalParallelism checks the parallel-query GUCs against each other and the CPU count.
//
//   - maxParallelPerGather == 0 with > 1 effective CPU → Info.
//   - maxParallelWorkers > effectiveCPUs → Info.
//   - maxParallelWorkers > maxWorkerProcesses → Info.
//
// cpus <= 0 means the CPU count is unknown (ErrNoOSFacts). The two CPU checks
// are then skipped and the worker-pool comparison still runs.
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html
// src: https://www.postgresql.org/docs/current/runtime-config-query.html#RUNTIME-CONFIG-QUERY-OTHER
func evalParallelism(maxWorkerProcesses, maxParallelWorkers, maxParallelPerGather int64, cpus float64) []pgtuner.Advice {
	var out []pgtuner.Advice

	// Only meaningful when more than one CPU is known.
	// src: https://www.postgresql.org/docs/current/runtime-config-query.html#RUNTIME-CONFIG-QUERY-OTHER
	if maxParallelPerGather == 0 && cpus > 1 {
		out = append(out, pgtuner.Advice{
			ID:      "config.parallel-query-disabled",
			Level:   pgtuner.Info,
			Section: configSection,
			Title:   "Parallel query is disabled",
			Message: fmt.Sprintf(
				"max_parallel_workers_per_gather=0 disables parallel query, but %.1f effective CPUs are available. "+
					"Large sequential scans, joins, and aggregates cannot use multiple CPUs.",
				cpus),
			Detail: "Set max_parallel_workers_per_gather to a small value (e.g. 2). This lets the planner run expensive scans in parallel. " +
				"The planner stays within the limits of max_parallel_workers and max_worker_processes.",
			DocRef: docRuntimeQueryParallel,
		})
	}

	// src: https://www.postgresql.org/docs/current/runtime-config-resource.html
	if cpus > 0 && float64(maxParallelWorkers) > cpus {
		out = append(out, pgtuner.Advice{
			ID:      "config.parallel-workers-exceed-cpus",
			Level:   pgtuner.Info,
			Section: configSection,
			Title:   "max_parallel_workers exceeds available CPUs",
			Message: fmt.Sprintf(
				"max_parallel_workers=%d exceeds the %.1f effective CPUs available. "+
					"If many parallel queries run concurrently, they will contend for CPU time. This increases latency instead of reducing it.",
				maxParallelWorkers, cpus),
			Detail: "Consider setting max_parallel_workers at or below the effective CPU count. " +
				"On a cgroup-constrained host, the CPU quota may be well below the physical core count.",
			DocRef: docRuntimeResource,
		})
	}

	// max_parallel_workers draws from the max_worker_processes pool, so a higher value grants nothing.
	// src: https://www.postgresql.org/docs/current/runtime-config-resource.html
	if maxWorkerProcesses > 0 && maxParallelWorkers > maxWorkerProcesses {
		out = append(out, pgtuner.Advice{
			ID:      "config.parallel-workers-exceed-worker-processes",
			Level:   pgtuner.Info,
			Section: configSection,
			Title:   "max_parallel_workers exceeds max_worker_processes",
			Message: fmt.Sprintf(
				"max_parallel_workers=%d exceeds max_worker_processes=%d. Parallel workers come from the "+
					"max_worker_processes pool. The effective parallel-worker ceiling is %d, not %d.",
				maxParallelWorkers, maxWorkerProcesses, maxWorkerProcesses, maxParallelWorkers),
			Detail: "Raise max_worker_processes (requires a restart) if you intend max_parallel_workers to be effective. " +
				"Or lower max_parallel_workers to match the pool. Remember that extensions and logical replication workers also share the pool.",
			DocRef: docRuntimeResource,
		})
	}

	return out
}

// logHygiene holds the four logging GUCs the hygiene sub-check evaluates.
type logHygiene struct {
	logMinDurationStatement int64 // -1 = slow-query logging disabled
	logLockWaits            bool  // true when 'on'
	logAutovacuumMinDur     int64 // -1 = autovacuum logging disabled
	logCheckpoints          bool  // true when 'on'
	logCheckpointsPresent   bool  // whether log_checkpoints was readable
	// checkpointsDefaultOn is true on PG15+. Below PG15 the default is off, so off is
	// likely not a deliberate choice.
	checkpointsDefaultOn bool
}

// evalLoggingHygiene emits an Info for each disabled diagnostic-logging GUC.
// It does not cover log_temp_files, which the Storage section owns.
// src: https://www.postgresql.org/docs/current/runtime-config-logging.html
func evalLoggingHygiene(h logHygiene) []pgtuner.Advice {
	var out []pgtuner.Advice

	// src: https://www.postgresql.org/docs/current/runtime-config-logging.html#GUC-LOG-MIN-DURATION-STATEMENT
	if h.logMinDurationStatement == -1 {
		out = append(out, pgtuner.Advice{
			ID:      "config.log-min-duration-disabled",
			Level:   pgtuner.Info,
			Section: configSection,
			Title:   "Slow-query logging is disabled",
			Message: "log_min_duration_statement=-1 disables slow-query logging, so the server does not log statements based on duration. " +
				"Without continuous external monitoring, this removes a key signal for diagnosing latency.",
			Detail: "Set log_min_duration_statement to a millisecond threshold (e.g. 1000 for queries slower than 1 s) " +
				"to log slow statements. Pair it with pg_stat_statements for aggregate analysis.",
			DocRef: docRuntimeLogging,
		})
	}

	// src: https://www.postgresql.org/docs/current/runtime-config-logging.html#GUC-LOG-LOCK-WAITS
	if !h.logLockWaits {
		out = append(out, pgtuner.Advice{
			ID:      "config.log-lock-waits-disabled",
			Level:   pgtuner.Info,
			Section: configSection,
			Title:   "log_lock_waits is disabled",
			Message: "log_lock_waits=off means the server does not log a session that waits longer than deadlock_timeout for a lock. " +
				"Lock-contention incidents leave no trace in the server log.",
			Detail: "Set log_lock_waits=on to log long lock waits. This helps diagnose blocking caused by " +
				"long transactions or idle-in-transaction sessions.",
			DocRef: docRuntimeLogging,
		})
	}

	// src: https://www.postgresql.org/docs/current/runtime-config-logging.html#GUC-LOG-AUTOVACUUM-MIN-DURATION
	if h.logAutovacuumMinDur == -1 {
		out = append(out, pgtuner.Advice{
			ID:      "config.log-autovacuum-disabled",
			Level:   pgtuner.Info,
			Section: configSection,
			Title:   "Autovacuum logging is disabled",
			Message: "log_autovacuum_min_duration=-1 disables autovacuum logging. You cannot see how often autovacuum runs, " +
				"how long it takes, or whether it keeps up with the dead-tuple load.",
			Detail: "Set log_autovacuum_min_duration to a millisecond threshold (e.g. 0 to log all, or 250 to log slow runs). " +
				"This makes autovacuum activity visible in the server log.",
			DocRef: docRuntimeLogging,
		})
	}

	// log_checkpoints defaults to off before PG15, where off is likely just the default.
	// From PG15 the default is on, so an explicit off is unusual.
	// src: https://www.postgresql.org/docs/current/runtime-config-logging.html#GUC-LOG-CHECKPOINTS
	// src: https://www.postgresql.org/docs/15/release-15.html (log_checkpoints default changed to on)
	if h.logCheckpointsPresent && !h.logCheckpoints {
		msg := "log_checkpoints=off means the server does not log checkpoint activity (timing, buffers written, WAL). " +
			"This hides I/O spikes caused by checkpoints."
		if !h.checkpointsDefaultOn {
			msg += " This is the default before PostgreSQL 15."
		}
		out = append(out, pgtuner.Advice{
			ID:      "config.log-checkpoints-disabled",
			Level:   pgtuner.Info,
			Section: configSection,
			Title:   "log_checkpoints is disabled",
			Message: msg,
			Detail: "Set log_checkpoints=on to log each checkpoint. This helps you correlate latency spikes with checkpoint " +
				"flushing. It also helps you tune checkpoint_timeout and max_wal_size. PostgreSQL 15+ enables it by default.",
			DocRef: docRuntimeLogging,
		})
	}

	return out
}

// timeoutSettings holds the four timeout GUCs. For all four, 0 means "no limit".
type timeoutSettings struct {
	statementTimeout       int64
	idleInTxnTimeout       int64
	lockTimeout            int64
	transactionTimeout     int64 // PG≥17 only
	transactionTimeoutSupp bool  // whether transaction_timeout applies (PG≥17)
}

// evalTimeouts emits an Info for each timeout left at 0. The transaction_timeout
// check applies only when transactionTimeoutSupp is set (PG17+).
// src: https://www.postgresql.org/docs/current/runtime-config-client.html
func evalTimeouts(t timeoutSettings) []pgtuner.Advice {
	var out []pgtuner.Advice

	// src: https://www.postgresql.org/docs/current/runtime-config-client.html#GUC-STATEMENT-TIMEOUT
	if t.statementTimeout == 0 {
		out = append(out, pgtuner.Advice{
			ID:      "config.statement-timeout-unset",
			Level:   pgtuner.Info,
			Section: configSection,
			Title:   "statement_timeout is unset (no ceiling)",
			Message: "statement_timeout=0 means there is no upper bound on how long a single statement may run. " +
				"A runaway or accidental query (e.g. a missing WHERE clause) can hold resources indefinitely.",
			Detail: "Consider setting a generous global ceiling (e.g. via ALTER ROLE / per-database). Override it per session " +
				"for legitimately long jobs. Avoid setting it too low cluster-wide, because this can break maintenance work.",
			DocRef: docRuntimeClient,
		})
	}

	// Pairs with the Workload idle-in-txn finding.
	// src: https://www.postgresql.org/docs/current/runtime-config-client.html#GUC-IDLE-IN-TRANSACTION-SESSION-TIMEOUT
	if t.idleInTxnTimeout == 0 {
		out = append(out, pgtuner.Advice{
			ID:      "config.idle-in-transaction-timeout-unset",
			Level:   pgtuner.Info,
			Section: configSection,
			Title:   "idle_in_transaction_session_timeout is unset",
			Message: "idle_in_transaction_session_timeout=0 means the server never terminates sessions left idle inside a " +
				"transaction. Such sessions hold locks and block autovacuum (see the Workload section).",
			Detail: "Consider setting idle_in_transaction_session_timeout (e.g. '5min') to terminate stale " +
				"idle-in-transaction sessions automatically. This protects vacuum and lock availability.",
			DocRef: docRuntimeClient,
		})
	}

	// src: https://www.postgresql.org/docs/current/runtime-config-client.html#GUC-LOCK-TIMEOUT
	if t.lockTimeout == 0 {
		out = append(out, pgtuner.Advice{
			ID:      "config.lock-timeout-unset",
			Level:   pgtuner.Info,
			Section: configSection,
			Title:   "lock_timeout is unset (no ceiling)",
			Message: "lock_timeout=0 means a statement that cannot acquire a lock immediately waits indefinitely. " +
				"This can cause cascading blocking, especially for DDL that needs an exclusive lock.",
			Detail: "Consider setting lock_timeout (e.g. for migration / DDL sessions). Then blocked statements fail fast " +
				"instead of queuing behind a long-held lock.",
			DocRef: docRuntimeClient,
		})
	}

	// src: https://www.postgresql.org/docs/17/runtime-config-client.html#GUC-TRANSACTION-TIMEOUT
	if t.transactionTimeoutSupp && t.transactionTimeout == 0 {
		out = append(out, pgtuner.Advice{
			ID:      "config.transaction-timeout-unset",
			Level:   pgtuner.Info,
			Section: configSection,
			Title:   "transaction_timeout is unset (PG17+, no ceiling)",
			Message: "transaction_timeout=0 means there is no upper bound on the total duration of a transaction " +
				"(introduced in PostgreSQL 17). A long-lived transaction can hold snapshots and locks, which blocks vacuum.",
			Detail: "Consider setting transaction_timeout to bound transaction duration cluster-wide or per role. " +
				"It complements statement_timeout (per statement) and idle_in_transaction_session_timeout (idle gaps).",
			DocRef: docRuntimeClient,
		})
	}

	return out
}

// archiverStatus holds the pg_stat_archiver fields. The Valid flags are false for NULL timestamps.
type archiverStatus struct {
	failedCount       int64
	lastFailedTime    time.Time
	lastFailedValid   bool
	lastArchivedTime  time.Time
	lastArchivedValid bool
}

// evalArchiver runs only when archive_mode is on. Archiving fails when failed_count > 0
// or the last failure is newer than the last success (or no success exists).
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ARCHIVER-VIEW
func evalArchiver(a archiverStatus) []pgtuner.Advice {
	failingNow := a.lastFailedValid &&
		(!a.lastArchivedValid || a.lastFailedTime.After(a.lastArchivedTime))

	if a.failedCount <= 0 && !failingNow {
		return nil
	}

	return []pgtuner.Advice{{
		ID:      "config.wal-archiving-failing",
		Level:   pgtuner.Warn,
		Section: configSection,
		Title:   "WAL archiving is failing",
		Message: fmt.Sprintf(
			"archive_mode is on but pg_stat_archiver reports archiving problems (failed_count=%d). "+
				"Failing WAL archiving breaks continuous backups and point-in-time recovery. Unarchived WAL "+
				"can accumulate in pg_wal until the volume fills.",
			a.failedCount),
		Detail: "Inspect the archive_command and the server log for the failure cause (permissions, full destination, " +
			"network). Verify with: SELECT archived_count, last_archived_time, failed_count, last_failed_time, last_failed_wal " +
			"FROM pg_stat_archiver. After you fix the cause, archiving resumes automatically.",
		DocRef: docPGStatArchiver,
	}}
}

// evalOutdatedExtensions flags extensions whose installed_version differs from
// default_version. Each entry is formatted as "name installed→default".
// src: https://www.postgresql.org/docs/current/sql-alterextension.html
func evalOutdatedExtensions(descs []string) []pgtuner.Advice {
	if len(descs) == 0 {
		return nil
	}
	return []pgtuner.Advice{{
		ID:      "config.extensions-outdated",
		Level:   pgtuner.Info,
		Section: configSection,
		Title:   fmt.Sprintf("Extensions are not at their default version (%d)", len(descs)),
		Message: fmt.Sprintf(
			"%d installed extension(s) have an installed_version that differs from the default_version in "+
				"the installed extension files: %s. The new version is available but is not applied.",
			len(descs), configCapList(descs)),
		Detail: "Update each extension with ALTER EXTENSION <name> UPDATE. Optionally add TO '<version>'. " +
			"This typically happens when a server or package upgrade installs newer extension SQL files. " +
			"Review the extension's changelog before you update production.",
		DocRef: docAlterExtension,
	}}
}

// ConfigCheck evaluates settings pending a restart, parallelism vs CPU budget,
// logging hygiene, client timeouts, WAL archiving health, and outdated extensions.
// A query failure yields one Unknown advice and the other sub-checks continue.
// It never panics and never returns an error.
type ConfigCheck struct{ clusterScoped }

func (ConfigCheck) Name() string { return "config" }

func (ConfigCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	var out []pgtuner.Advice

	out = append(out, runPendingRestart(ctx, p)...)
	out = append(out, runParallelism(p)...)
	out = append(out, runLoggingHygiene(p)...)
	out = append(out, runTimeouts(p)...)
	out = append(out, runArchiver(ctx, p)...)
	out = append(out, runOutdatedExtensions(ctx, p)...)

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			ID:      "config.depth-ok",
			Level:   pgtuner.OK,
			Section: configSection,
			Title:   "Configuration depth looks healthy",
			Message: "No configuration concerns detected. No settings are pending restart. Parallelism is sane for the CPU budget. " +
				"Diagnostic logging is enabled. Timeouts have ceilings (or archiving is off). WAL archiving is healthy. " +
				"All extensions are at their default version.",
			DocRef: docPGSettings,
		})
	}
	return out
}

// runPendingRestart queries pg_settings for GUCs whose new value awaits a restart.
// src: https://www.postgresql.org/docs/current/view-pg-settings.html
func runPendingRestart(ctx context.Context, p *Probe) []pgtuner.Advice {
	const sql = `SELECT name FROM pg_settings WHERE pending_restart ORDER BY name`
	rows, err := p.Conn.Query(ctx, sql)
	if err != nil {
		return []pgtuner.Advice{{
			ID:      "config.pending-restart-query-failed",
			Level:   pgtuner.Unknown,
			Section: configSection,
			Title:   "Pending-restart query failed",
			Message: fmt.Sprintf("Could not query pg_settings for pending_restart: %v", err),
			DocRef:  docPGSettings,
		}}
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return []pgtuner.Advice{{
				ID:      "config.pending-restart-scan-failed",
				Level:   pgtuner.Unknown,
				Section: configSection,
				Title:   "Pending-restart scan failed",
				Message: fmt.Sprintf("Could not scan pg_settings row: %v", err),
				DocRef:  docPGSettings,
			}}
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		return []pgtuner.Advice{{
			ID:      "config.pending-restart-iteration-failed",
			Level:   pgtuner.Unknown,
			Section: configSection,
			Title:   "Pending-restart iteration failed",
			Message: fmt.Sprintf("Error iterating pg_settings rows: %v", err),
			DocRef:  docPGSettings,
		}}
	}
	return evalPendingRestart(names)
}

// runParallelism degrades to an unknown CPU count on ErrNoOSFacts.
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html
func runParallelism(p *Probe) []pgtuner.Advice {
	mwp, _ := p.Settings.Int("max_worker_processes")
	mpw, _ := p.Settings.Int("max_parallel_workers")
	mppg, _ := p.Settings.Int("max_parallel_workers_per_gather")

	cpus := -1.0 // -1 means unknown
	if eff, _, err := p.OS.EffectiveCPUs(); err == nil {
		cpus = eff
	}
	return evalParallelism(mwp, mpw, mppg, cpus)
}

// src: https://www.postgresql.org/docs/current/runtime-config-logging.html
func runLoggingHygiene(p *Probe) []pgtuner.Advice {
	lmds, _ := p.Settings.Int("log_min_duration_statement")
	lavd, _ := p.Settings.Int("log_autovacuum_min_duration")
	_, lcPresent := p.Settings.Get("log_checkpoints")

	h := logHygiene{
		logMinDurationStatement: lmds,
		logLockWaits:            p.Settings.Bool("log_lock_waits"),
		logAutovacuumMinDur:     lavd,
		logCheckpoints:          p.Settings.Bool("log_checkpoints"),
		logCheckpointsPresent:   lcPresent,
		checkpointsDefaultOn:    p.Version.AtLeast(15, 0),
	}
	return evalLoggingHygiene(h)
}

// runTimeouts evaluates transaction_timeout only on PG17+.
// src: https://www.postgresql.org/docs/current/runtime-config-client.html
func runTimeouts(p *Probe) []pgtuner.Advice {
	st, _ := p.Settings.Int("statement_timeout")
	iit, _ := p.Settings.Int("idle_in_transaction_session_timeout")
	lt, _ := p.Settings.Int("lock_timeout")

	t := timeoutSettings{
		statementTimeout: st,
		idleInTxnTimeout: iit,
		lockTimeout:      lt,
	}
	if p.Version.AtLeast(17, 0) {
		tt, _ := p.Settings.Int("transaction_timeout")
		t.transactionTimeout = tt
		t.transactionTimeoutSupp = true
	}
	return evalTimeouts(t)
}

// runArchiver queries pg_stat_archiver only when archive_mode is 'on' or 'always' (standby).
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ARCHIVER-VIEW
func runArchiver(ctx context.Context, p *Probe) []pgtuner.Advice {
	am, ok := p.Settings.Get("archive_mode")
	if !ok || (am != "on" && am != "always") {
		return nil
	}

	// last_failed_time and last_archived_time are nullable, so scan into pointers.
	const sql = `SELECT failed_count, last_failed_time, last_archived_time FROM pg_stat_archiver`
	var failedCount int64
	var lastFailed, lastArchived *time.Time
	if err := p.Conn.QueryRow(ctx, sql).Scan(&failedCount, &lastFailed, &lastArchived); err != nil {
		return []pgtuner.Advice{{
			ID:      "config.archiver-query-failed",
			Level:   pgtuner.Unknown,
			Section: configSection,
			Title:   "Archiver-status query failed",
			Message: fmt.Sprintf("Could not query pg_stat_archiver: %v", err),
			DocRef:  docPGStatArchiver,
		}}
	}

	a := archiverStatus{failedCount: failedCount}
	if lastFailed != nil {
		a.lastFailedTime = *lastFailed
		a.lastFailedValid = true
	}
	if lastArchived != nil {
		a.lastArchivedTime = *lastArchived
		a.lastArchivedValid = true
	}
	return evalArchiver(a)
}

// src: https://www.postgresql.org/docs/current/sql-alterextension.html
func runOutdatedExtensions(ctx context.Context, p *Probe) []pgtuner.Advice {
	const sql = `SELECT name, installed_version, default_version
		FROM pg_available_extensions
		WHERE installed_version IS NOT NULL
		  AND installed_version <> default_version
		ORDER BY name`
	rows, err := p.Conn.Query(ctx, sql)
	if err != nil {
		return []pgtuner.Advice{{
			ID:      "config.extensions-query-failed",
			Level:   pgtuner.Unknown,
			Section: configSection,
			Title:   "Outdated-extensions query failed",
			Message: fmt.Sprintf("Could not query pg_available_extensions: %v", err),
			DocRef:  docAlterExtension,
		}}
	}
	defer rows.Close()

	var descs []string
	for rows.Next() {
		var name, installed, def string
		if err := rows.Scan(&name, &installed, &def); err != nil {
			return []pgtuner.Advice{{
				ID:      "config.extensions-scan-failed",
				Level:   pgtuner.Unknown,
				Section: configSection,
				Title:   "Outdated-extensions scan failed",
				Message: fmt.Sprintf("Could not scan pg_available_extensions row: %v", err),
				DocRef:  docAlterExtension,
			}}
		}
		descs = append(descs, fmt.Sprintf("%s (%s→%s)", name, installed, def))
	}
	if err := rows.Err(); err != nil {
		return []pgtuner.Advice{{
			ID:      "config.extensions-iteration-failed",
			Level:   pgtuner.Unknown,
			Section: configSection,
			Title:   "Outdated-extensions iteration failed",
			Message: fmt.Sprintf("Error iterating pg_available_extensions rows: %v", err),
			DocRef:  docAlterExtension,
		}}
	}
	return evalOutdatedExtensions(descs)
}
