package checks

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// docPGSettings is the canonical reference for the pg_settings view, including
// the pending_restart column.
// src: https://www.postgresql.org/docs/current/view-pg-settings.html
const docPGSettings = "https://www.postgresql.org/docs/current/view-pg-settings.html"

// docRuntimeResource is the canonical reference for resource/worker GUCs
// (max_worker_processes, max_parallel_workers).
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html
const docRuntimeResource = "https://www.postgresql.org/docs/current/runtime-config-resource.html"

// docRuntimeQueryParallel is the canonical reference for parallel-query planner
// GUCs (max_parallel_workers_per_gather).
// src: https://www.postgresql.org/docs/current/runtime-config-query.html#RUNTIME-CONFIG-QUERY-OTHER
const docRuntimeQueryParallel = "https://www.postgresql.org/docs/current/runtime-config-query.html#RUNTIME-CONFIG-QUERY-OTHER"

// docRuntimeLogging is the canonical reference for the logging-hygiene GUCs.
// src: https://www.postgresql.org/docs/current/runtime-config-logging.html
const docRuntimeLogging = "https://www.postgresql.org/docs/current/runtime-config-logging.html"

// docRuntimeClient is the canonical reference for the client-connection timeout
// GUCs (statement_timeout, idle_in_transaction_session_timeout, lock_timeout,
// and PG≥17 transaction_timeout).
// src: https://www.postgresql.org/docs/current/runtime-config-client.html
const docRuntimeClient = "https://www.postgresql.org/docs/current/runtime-config-client.html"

// docPGStatArchiver is the canonical reference for the pg_stat_archiver view.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ARCHIVER-VIEW
const docPGStatArchiver = "https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ARCHIVER-VIEW"

// docAlterExtension is the canonical reference for ALTER EXTENSION ... UPDATE.
// src: https://www.postgresql.org/docs/current/sql-alterextension.html
const docAlterExtension = "https://www.postgresql.org/docs/current/sql-alterextension.html"

// configSection is the section name emitted for all ConfigCheck advice items.
const configSection = "Configuration"

// configListCap is the maximum number of names (settings, extensions) included
// in an advice message before the remainder is summarised as "… and N more".
const configListCap = 10

// ----- pure eval functions ---------------------------------------------------

// configCapList joins up to configListCap names with a summarised remainder,
// reusing the shared capList helper (defined in schema.go).
func configCapList(names []string) string {
	capped, suffix := capList(names, configListCap)
	return strings.Join(capped, ", ") + suffix
}

// evalPendingRestart is the pure decision function for the pending-restart
// sub-check.  A non-empty list of names means one or more GUCs were changed
// (e.g. via ALTER SYSTEM) but require a server restart to take effect; the
// running configuration does not yet match the configured one.
// src: https://www.postgresql.org/docs/current/view-pg-settings.html
func evalPendingRestart(names []string) []pgtuner.Advice {
	if len(names) == 0 {
		return nil
	}
	return []pgtuner.Advice{{
		Level:   pgtuner.Warn,
		Section: configSection,
		Title:   fmt.Sprintf("Settings changed but pending restart (%d)", len(names)),
		Message: fmt.Sprintf(
			"%d setting(s) have a new value configured that has not taken effect because it requires a server restart "+
				"(pg_settings.pending_restart is true): %s. The running server is using the old values.",
			len(names), configCapList(names)),
		Detail: "Restart the PostgreSQL server during a maintenance window to apply these settings. " +
			"Verify with: SELECT name, setting, pending_restart FROM pg_settings WHERE pending_restart.",
		DocRef: docPGSettings,
	}}
}

// evalParallelism is the pure decision function for the parallelism-vs-CPUs
// sub-check.  It evaluates the worker-pool / parallel-query GUCs against each
// other and (when available) the effective CPU count.
//
//   - maxParallelPerGather == 0 on a box with > 1 effective CPU → Info
//     (parallel query is disabled; multi-core CPU is underused for big scans).
//   - maxParallelWorkers > effectiveCPUs → Info (oversubscribed CPU budget).
//   - maxParallelWorkers > maxWorkerProcesses → Info (parallel workers are
//     capped by the shared background-worker pool).
//
// cpus <= 0 means the effective CPU count is unknown (ErrNoOSFacts); the two
// CPU-relative checks are then skipped, but the worker-pool comparison (which
// does not need CPU facts) still runs.
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html
// src: https://www.postgresql.org/docs/current/runtime-config-query.html#RUNTIME-CONFIG-QUERY-OTHER
func evalParallelism(maxWorkerProcesses, maxParallelWorkers, maxParallelPerGather int64, cpus float64) []pgtuner.Advice {
	var out []pgtuner.Advice

	// Parallel query disabled on a multi-CPU box.  Only meaningful when we know
	// there is more than one CPU to exploit.
	// src: https://www.postgresql.org/docs/current/runtime-config-query.html#RUNTIME-CONFIG-QUERY-OTHER
	if maxParallelPerGather == 0 && cpus > 1 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: configSection,
			Title:   "Parallel query is disabled",
			Message: fmt.Sprintf(
				"max_parallel_workers_per_gather=0 disables parallel query, but %.1f effective CPUs are available. "+
					"Large sequential scans, joins, and aggregates cannot use multiple CPUs.",
				cpus),
			Detail: "Set max_parallel_workers_per_gather to a small value (e.g. 2) to allow the planner to parallelise expensive scans, " +
				"bounded by max_parallel_workers and max_worker_processes.",
			DocRef: docRuntimeQueryParallel,
		})
	}

	// Parallel workers oversubscribe the CPU budget.
	// src: https://www.postgresql.org/docs/current/runtime-config-resource.html
	if cpus > 0 && float64(maxParallelWorkers) > cpus {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: configSection,
			Title:   "max_parallel_workers exceeds available CPUs",
			Message: fmt.Sprintf(
				"max_parallel_workers=%d exceeds the %.1f effective CPUs available. "+
					"If many parallel queries run concurrently they will contend for CPU time, increasing latency rather than reducing it.",
				maxParallelWorkers, cpus),
			Detail: "Consider setting max_parallel_workers at or below the effective CPU count. " +
				"On a cgroup-constrained host the CPU quota may be well below the physical core count.",
			DocRef: docRuntimeResource,
		})
	}

	// Parallel workers capped by the shared background-worker pool.
	// max_parallel_workers draws from the max_worker_processes pool; setting it
	// higher than the pool cannot grant more workers.
	// src: https://www.postgresql.org/docs/current/runtime-config-resource.html
	if maxWorkerProcesses > 0 && maxParallelWorkers > maxWorkerProcesses {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: configSection,
			Title:   "max_parallel_workers exceeds max_worker_processes",
			Message: fmt.Sprintf(
				"max_parallel_workers=%d exceeds max_worker_processes=%d. Parallel workers are allocated from the "+
					"max_worker_processes pool, so the effective parallel-worker ceiling is %d, not %d.",
				maxParallelWorkers, maxWorkerProcesses, maxWorkerProcesses, maxParallelWorkers),
			Detail: "Raise max_worker_processes (requires a restart) if you intend max_parallel_workers to be effective, " +
				"or lower max_parallel_workers to match the pool. Remember the pool is also shared with extensions and logical replication workers.",
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
	// checkpointsDefaultOn reports whether the server version defaults
	// log_checkpoints to on (PG≥15).  Below PG15 the default is off, so an
	// off value is the default rather than a deliberate choice and merits a
	// gentle Info.
	checkpointsDefaultOn bool
}

// evalLoggingHygiene is the pure decision function for the logging-hygiene
// sub-check.  Each disabled diagnostic-logging GUC produces an Info.  It does
// NOT touch log_temp_files (owned by the Storage section).
// src: https://www.postgresql.org/docs/current/runtime-config-logging.html
func evalLoggingHygiene(h logHygiene) []pgtuner.Advice {
	var out []pgtuner.Advice

	// log_min_duration_statement = -1 disables slow-query logging entirely.
	// src: https://www.postgresql.org/docs/current/runtime-config-logging.html#GUC-LOG-MIN-DURATION-STATEMENT
	if h.logMinDurationStatement == -1 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: configSection,
			Title:   "Slow-query logging is disabled",
			Message: "log_min_duration_statement=-1 disables slow-query logging, so no statements are logged based on duration. " +
				"This removes a key signal for diagnosing latency without continuous external monitoring.",
			Detail: "Set log_min_duration_statement to a millisecond threshold (e.g. 1000 for queries slower than 1 s) " +
				"to log slow statements. Pair with pg_stat_statements for aggregate analysis.",
			DocRef: docRuntimeLogging,
		})
	}

	// log_lock_waits off means sessions blocked past deadlock_timeout are not
	// logged, hiding lock-contention problems.
	// src: https://www.postgresql.org/docs/current/runtime-config-logging.html#GUC-LOG-LOCK-WAITS
	if !h.logLockWaits {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: configSection,
			Title:   "log_lock_waits is disabled",
			Message: "log_lock_waits=off means a session waiting longer than deadlock_timeout for a lock is not logged. " +
				"Lock-contention incidents leave no trace in the server log.",
			Detail: "Set log_lock_waits=on to log long lock waits, which helps diagnose blocking caused by " +
				"long transactions or idle-in-transaction sessions.",
			DocRef: docRuntimeLogging,
		})
	}

	// log_autovacuum_min_duration = -1 disables autovacuum logging, hiding how
	// often and how long autovacuum runs.
	// src: https://www.postgresql.org/docs/current/runtime-config-logging.html#GUC-LOG-AUTOVACUUM-MIN-DURATION
	if h.logAutovacuumMinDur == -1 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: configSection,
			Title:   "Autovacuum logging is disabled",
			Message: "log_autovacuum_min_duration=-1 disables autovacuum logging. You cannot see how often autovacuum runs, " +
				"how long it takes, or whether it is keeping up with the dead-tuple load.",
			Detail: "Set log_autovacuum_min_duration to a millisecond threshold (e.g. 0 to log all, or 250 to log slow runs) " +
				"to make autovacuum activity visible in the server log.",
			DocRef: docRuntimeLogging,
		})
	}

	// log_checkpoints: in PG<15 the default is off; an off value there is likely
	// just the default and worth a gentle Info.  In PG≥15 the default is on, so
	// an explicit off is unusual and equally worth an Info.
	// src: https://www.postgresql.org/docs/current/runtime-config-logging.html#GUC-LOG-CHECKPOINTS
	// src: https://www.postgresql.org/docs/15/release-15.html (log_checkpoints default changed to on)
	if h.logCheckpointsPresent && !h.logCheckpoints {
		msg := "log_checkpoints=off means checkpoint activity (timing, buffers written, WAL) is not logged, " +
			"hiding I/O spikes caused by checkpoints."
		if !h.checkpointsDefaultOn {
			msg += " This is the default before PostgreSQL 15."
		}
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: configSection,
			Title:   "log_checkpoints is disabled",
			Message: msg,
			Detail: "Set log_checkpoints=on to log each checkpoint. This helps correlate latency spikes with checkpoint " +
				"flushing and to tune checkpoint_timeout / max_wal_size. PostgreSQL 15+ enables it by default.",
			DocRef: docRuntimeLogging,
		})
	}

	return out
}

// timeoutSettings holds the four timeout GUCs the timeout sub-check evaluates.
// Each value is the configured number; 0 means "no limit" for all four.
type timeoutSettings struct {
	statementTimeout       int64
	idleInTxnTimeout       int64
	lockTimeout            int64
	transactionTimeout     int64 // PG≥17 only
	transactionTimeoutSupp bool  // whether transaction_timeout applies (PG≥17)
}

// evalTimeouts is the pure decision function for the timeouts sub-check.  Each
// timeout left at 0 (no limit) produces an Info recommending a ceiling.  The
// transaction_timeout check is gated on PG≥17 via transactionTimeoutSupp.
// src: https://www.postgresql.org/docs/current/runtime-config-client.html
func evalTimeouts(t timeoutSettings) []pgtuner.Advice {
	var out []pgtuner.Advice

	// statement_timeout = 0 → no per-statement ceiling; a runaway query can run
	// indefinitely.
	// src: https://www.postgresql.org/docs/current/runtime-config-client.html#GUC-STATEMENT-TIMEOUT
	if t.statementTimeout == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: configSection,
			Title:   "statement_timeout is unset (no ceiling)",
			Message: "statement_timeout=0 means there is no upper bound on how long a single statement may run. " +
				"A runaway or accidental query (e.g. a missing WHERE clause) can hold resources indefinitely.",
			Detail: "Consider setting a generous global ceiling (e.g. via ALTER ROLE / per-database) and overriding it per session " +
				"for legitimately long jobs. Avoid setting it cluster-wide too low, which can break maintenance work.",
			DocRef: docRuntimeClient,
		})
	}

	// idle_in_transaction_session_timeout = 0 → idle-in-transaction sessions are
	// never auto-terminated.  Pairs with the Workload idle-in-txn finding.
	// src: https://www.postgresql.org/docs/current/runtime-config-client.html#GUC-IDLE-IN-TRANSACTION-SESSION-TIMEOUT
	if t.idleInTxnTimeout == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: configSection,
			Title:   "idle_in_transaction_session_timeout is unset",
			Message: "idle_in_transaction_session_timeout=0 means sessions left idle inside a transaction are never " +
				"auto-terminated. Such sessions hold locks and block autovacuum (see the Workload section).",
			Detail: "Consider setting idle_in_transaction_session_timeout (e.g. '5min') to automatically terminate stale " +
				"idle-in-transaction sessions and protect vacuum and lock availability.",
			DocRef: docRuntimeClient,
		})
	}

	// lock_timeout = 0 → a statement waiting on a lock waits forever.
	// src: https://www.postgresql.org/docs/current/runtime-config-client.html#GUC-LOCK-TIMEOUT
	if t.lockTimeout == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: configSection,
			Title:   "lock_timeout is unset (no ceiling)",
			Message: "lock_timeout=0 means a statement that cannot immediately acquire a lock will wait indefinitely. " +
				"This can cause cascading blocking, especially for DDL that needs an exclusive lock.",
			Detail: "Consider setting lock_timeout (e.g. for migration / DDL sessions) so blocked statements fail fast " +
				"rather than queuing behind a long-held lock.",
			DocRef: docRuntimeClient,
		})
	}

	// transaction_timeout = 0 (PG≥17 only) → whole transactions have no ceiling.
	// src: https://www.postgresql.org/docs/17/runtime-config-client.html#GUC-TRANSACTION-TIMEOUT
	if t.transactionTimeoutSupp && t.transactionTimeout == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: configSection,
			Title:   "transaction_timeout is unset (PG17+, no ceiling)",
			Message: "transaction_timeout=0 means there is no upper bound on the total duration of a transaction " +
				"(introduced in PostgreSQL 17). A long-lived transaction can hold snapshots and locks, blocking vacuum.",
			Detail: "Consider setting transaction_timeout to bound transaction duration cluster-wide or per role. " +
				"It complements statement_timeout (per statement) and idle_in_transaction_session_timeout (idle gaps).",
			DocRef: docRuntimeClient,
		})
	}

	return out
}

// archiverStatus holds the pg_stat_archiver fields the archiving sub-check
// evaluates.  lastFailedValid/lastArchivedValid report whether the
// corresponding timestamp was non-NULL.
type archiverStatus struct {
	failedCount       int64
	lastFailedTime    time.Time
	lastFailedValid   bool
	lastArchivedTime  time.Time
	lastArchivedValid bool
}

// evalArchiver is the pure decision function for the WAL-archiving sub-check.
// It is only invoked when archive_mode is on.  Archiving is considered failing
// when there have been any failures (failed_count > 0) OR the most recent
// failure is newer than the most recent success (or there has been no success
// at all while a failure exists).
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ARCHIVER-VIEW
func evalArchiver(a archiverStatus) []pgtuner.Advice {
	// A failure is "current" when the last failure is more recent than the last
	// success, or there has been no success at all.
	failingNow := a.lastFailedValid &&
		(!a.lastArchivedValid || a.lastFailedTime.After(a.lastArchivedTime))

	if a.failedCount <= 0 && !failingNow {
		return nil
	}

	return []pgtuner.Advice{{
		Level:   pgtuner.Warn,
		Section: configSection,
		Title:   "WAL archiving is failing",
		Message: fmt.Sprintf(
			"archive_mode is on but pg_stat_archiver reports archiving problems (failed_count=%d). "+
				"Failing WAL archiving means continuous backups / point-in-time recovery are broken and unarchived WAL "+
				"can accumulate in pg_wal until the volume fills.",
			a.failedCount),
		Detail: "Inspect the archive_command and the server log for the failure cause (permissions, full destination, " +
			"network). Verify with: SELECT archived_count, last_archived_time, failed_count, last_failed_time, last_failed_wal " +
			"FROM pg_stat_archiver. After fixing the cause, archiving resumes automatically.",
		DocRef: docPGStatArchiver,
	}}
}

// evalOutdatedExtensions is the pure decision function for the
// outdated-extensions sub-check.  Each name is formatted as
// "name installed→default".  Any installed extension whose installed_version
// differs from default_version can be updated with ALTER EXTENSION ... UPDATE.
// src: https://www.postgresql.org/docs/current/sql-alterextension.html
func evalOutdatedExtensions(descs []string) []pgtuner.Advice {
	if len(descs) == 0 {
		return nil
	}
	return []pgtuner.Advice{{
		Level:   pgtuner.Info,
		Section: configSection,
		Title:   fmt.Sprintf("Extensions are not at their default version (%d)", len(descs)),
		Message: fmt.Sprintf(
			"%d installed extension(s) have an installed_version that differs from the default_version provided by "+
				"the installed extension files: %s. The new version is available but has not been applied.",
			len(descs), configCapList(descs)),
		Detail: "Update each with: ALTER EXTENSION <name> UPDATE; (optionally TO '<version>'). " +
			"This typically happens after a server/package upgrade installed newer extension SQL files. " +
			"Review the extension's changelog before updating production.",
		DocRef: docAlterExtension,
	}}
}

// ----- ConfigCheck ------------------------------------------------------------

// ConfigCheck evaluates configuration-depth concerns: settings pending a
// restart, parallelism vs CPU budget, logging hygiene, client timeouts, WAL
// archiving health, and outdated extensions.  Each sub-check is independent; a
// query failure yields a single Unknown-level advice for that sub-check while
// siblings continue.  It never panics and never returns an error.
type ConfigCheck struct{ clusterScoped }

func (ConfigCheck) Name() string { return "config" }

// Run executes all configuration-depth sub-checks.
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
			Level:   pgtuner.OK,
			Section: configSection,
			Title:   "Configuration depth looks healthy",
			Message: "No configuration concerns detected: no settings pending restart, parallelism is sane for the CPU budget, " +
				"diagnostic logging is enabled, timeouts have ceilings (or archiving is off), WAL archiving is healthy, " +
				"and all extensions are at their default version.",
			DocRef: docPGSettings,
		})
	}
	return out
}

// runPendingRestart queries pg_settings for GUCs whose new value awaits a
// restart.
// src: https://www.postgresql.org/docs/current/view-pg-settings.html
func runPendingRestart(ctx context.Context, p *Probe) []pgtuner.Advice {
	const sql = `SELECT name FROM pg_settings WHERE pending_restart ORDER BY name`
	rows, err := p.Conn.Query(ctx, sql)
	if err != nil {
		return []pgtuner.Advice{{
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
			Level:   pgtuner.Unknown,
			Section: configSection,
			Title:   "Pending-restart iteration failed",
			Message: fmt.Sprintf("Error iterating pg_settings rows: %v", err),
			DocRef:  docPGSettings,
		}}
	}
	return evalPendingRestart(names)
}

// runParallelism reads the worker/parallel GUCs from the Settings snapshot and
// the effective CPU count from the OS inspector (degrading on ErrNoOSFacts).
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html
func runParallelism(p *Probe) []pgtuner.Advice {
	mwp, _ := p.Settings.Int("max_worker_processes")
	mpw, _ := p.Settings.Int("max_parallel_workers")
	mppg, _ := p.Settings.Int("max_parallel_workers_per_gather")

	cpus := -1.0 // -1 ⇒ unknown; evalParallelism skips CPU-relative checks
	if eff, _, err := p.OS.EffectiveCPUs(); err == nil {
		cpus = eff
	}
	return evalParallelism(mwp, mpw, mppg, cpus)
}

// runLoggingHygiene reads the logging GUCs from the Settings snapshot.
// log_checkpoints defaults to on from PG15.
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

// runTimeouts reads the timeout GUCs from the Settings snapshot.
// transaction_timeout is only evaluated on PG≥17.
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

// runArchiver checks WAL archiving health.  It only queries pg_stat_archiver
// when archive_mode is on (a value of 'on' or, on a standby, 'always').
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ARCHIVER-VIEW
func runArchiver(ctx context.Context, p *Probe) []pgtuner.Advice {
	am, ok := p.Settings.Get("archive_mode")
	if !ok || (am != "on" && am != "always") {
		// archive_mode off → no archiving advice.
		return nil
	}

	// last_failed_time and last_archived_time are nullable; scan into pointers so
	// a NULL is observable rather than an error.
	// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ARCHIVER-VIEW
	const sql = `SELECT failed_count, last_failed_time, last_archived_time FROM pg_stat_archiver`
	var failedCount int64
	var lastFailed, lastArchived *time.Time
	if err := p.Conn.QueryRow(ctx, sql).Scan(&failedCount, &lastFailed, &lastArchived); err != nil {
		return []pgtuner.Advice{{
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

// runOutdatedExtensions queries pg_available_extensions for installed
// extensions whose installed_version differs from default_version.
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
			Level:   pgtuner.Unknown,
			Section: configSection,
			Title:   "Outdated-extensions iteration failed",
			Message: fmt.Sprintf("Error iterating pg_available_extensions rows: %v", err),
			DocRef:  docAlterExtension,
		}}
	}
	return evalOutdatedExtensions(descs)
}
