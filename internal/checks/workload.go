package checks

import (
	"context"
	"fmt"

	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// docPGStatActivity is the canonical reference for pg_stat_activity columns.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
const docPGStatActivity = "https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW"

// docIdleInTransactionTimeout is the canonical reference for
// idle_in_transaction_session_timeout.
// src: https://www.postgresql.org/docs/current/runtime-config-client.html#GUC-IDLE-IN-TRANSACTION-SESSION-TIMEOUT
const docIdleInTransactionTimeout = "https://www.postgresql.org/docs/current/runtime-config-client.html#GUC-IDLE-IN-TRANSACTION-SESSION-TIMEOUT"

// docPGBlockingPids is the canonical reference for pg_blocking_pids().
// src: https://www.postgresql.org/docs/current/functions-info.html#FUNCTIONS-PG-BLOCKING-PIDS
const docPGBlockingPids = "https://www.postgresql.org/docs/current/functions-info.html#FUNCTIONS-PG-BLOCKING-PIDS"

// docPGPreparedXacts is the canonical reference for pg_prepared_xacts.
// src: https://www.postgresql.org/docs/current/view-pg-prepared-xacts.html
const docPGPreparedXacts = "https://www.postgresql.org/docs/current/view-pg-prepared-xacts.html"

// workloadSection is the section name emitted for all WorkloadCheck advice items.
const workloadSection = "Workload"

// connUtilizationWarnThreshold is the fraction of max_connections above which we
// emit a Warn. At 80 % the server is close to exhausting backend slots, which
// causes connection errors for new clients.
// Threshold derived from community guidance: backends consume shared memory and
// process slots; exhausting max_connections causes FATAL "sorry, too many clients"
// errors. 80 % is a conservative early-warning boundary.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
// src: https://www.postgresql.org/docs/current/runtime-config-connection.html#GUC-MAX-CONNECTIONS
const connUtilizationWarnThreshold = 0.80

// connUtilizationInfoThreshold is the fraction of max_connections above which we
// emit an Info recommending a connection pooler.
// At 60 % the server has meaningful load; a pooler prevents saturation under
// traffic spikes without requiring max_connections to be raised further.
// src: https://www.postgresql.org/docs/current/runtime-config-connection.html#GUC-MAX-CONNECTIONS
const connUtilizationInfoThreshold = 0.60

// idleInTransactionOldestWarnSecs is the age in seconds of the oldest
// idle-in-transaction session above which we upgrade the advice to Warn.
// 300 s (5 minutes) is the default value recommended for
// idle_in_transaction_session_timeout in the PG documentation.  A session that
// has been idle-in-transaction for more than 5 minutes is almost certainly
// stale, holds row-level locks, and blocks autovacuum.
// src: https://www.postgresql.org/docs/current/runtime-config-client.html#GUC-IDLE-IN-TRANSACTION-SESSION-TIMEOUT
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
const idleInTransactionOldestWarnSecs = 300.0

// longRunningQueryOldestInfoSecs is the age in seconds of the oldest active
// client-backend query above which we emit an Info.  5 minutes (300 s) is a
// reasonable signal that a query may be unexpectedly long; operators should
// review it.  This is a soft threshold for informational purposes only.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
const longRunningQueryOldestInfoSecs = 300.0

// ----- pure eval functions ---------------------------------------------------

// evalConnUtilization is the pure decision function for connection-utilization
// advice.  It takes the number of active client backends (used) and the
// max_connections setting (max) and returns at most one Advice.
//
//   - used/max > 80 % → Warn (near exhaustion; connections will be rejected)
//   - used/max > 60 % → Info (consider a connection pooler)
//   - else            → nil
//
// Note: results reflect a point-in-time snapshot of pg_stat_activity.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
// src: https://www.postgresql.org/docs/current/runtime-config-connection.html#GUC-MAX-CONNECTIONS
func evalConnUtilization(used, max int64) *pgtuner.Advice {
	if max <= 0 {
		return nil
	}
	ratio := float64(used) / float64(max)
	switch {
	case ratio > connUtilizationWarnThreshold:
		a := pgtuner.Advice{
			Level:   pgtuner.Warn,
			Section: workloadSection,
			Title:   "Connection utilization is high (>80%)",
			Message: fmt.Sprintf(
				"This is a point-in-time snapshot: %d of %d max_connections (%.1f%%) are in use by client backends. "+
					"At this level, new connections may be rejected with \"sorry, too many clients\" under burst load.",
				used, max, ratio*100),
			Detail: "Consider deploying a connection pooler (e.g. PgBouncer) to multiplex application connections. " +
				"Alternatively, reduce long-lived idle connections or increase max_connections if RAM allows.",
			DocRef: docPGStatActivity,
		}
		return &a
	case ratio > connUtilizationInfoThreshold:
		a := pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: workloadSection,
			Title:   "Connection utilization is moderate (>60%)",
			Message: fmt.Sprintf(
				"This is a point-in-time snapshot: %d of %d max_connections (%.1f%%) are in use by client backends. "+
					"A connection pooler is recommended before utilization reaches the 80%% warning level.",
				used, max, ratio*100),
			Detail: "A connection pooler (e.g. PgBouncer in transaction mode) can serve many more application connections " +
				"than backends while using far fewer server-side processes.",
			DocRef: docPGStatActivity,
		}
		return &a
	default:
		return nil
	}
}

// evalIdleInTransaction is the pure decision function for idle-in-transaction
// session advice.  Both 'idle in transaction' and 'idle in transaction (aborted)'
// sessions hold row-level locks and prevent autovacuum from cleaning dead tuples
// on any table they have touched.  The aborted variant additionally cannot commit
// and must be rolled back; it is equally harmful for lock retention and vacuum.
//
//   - count > 0 AND oldest > 300 s → Warn (long-held locks / vacuum blockage)
//   - count > 0 (younger)          → Info (snapshot shows idle-in-txn sessions)
//   - count == 0                   → nil
//
// Note: results reflect a point-in-time snapshot of pg_stat_activity.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
// src: https://www.postgresql.org/docs/current/runtime-config-client.html#GUC-IDLE-IN-TRANSACTION-SESSION-TIMEOUT
func evalIdleInTransaction(count int64, oldestSecs float64) *pgtuner.Advice {
	if count <= 0 {
		return nil
	}
	if oldestSecs > idleInTransactionOldestWarnSecs {
		a := pgtuner.Advice{
			Level:   pgtuner.Warn,
			Section: workloadSection,
			Title:   fmt.Sprintf("Idle-in-transaction sessions detected (%d) — oldest >5 min", count),
			Message: fmt.Sprintf(
				"This is a point-in-time snapshot: %d session(s) are in state 'idle in transaction' or "+
					"'idle in transaction (aborted)'; "+
					"the oldest has been idle-in-transaction for %.0f seconds (%.1f minutes). "+
					"These sessions hold row-level locks and prevent autovacuum from reclaiming dead tuples on touched tables.",
				count, oldestSecs, oldestSecs/60),
			Detail: "Identify the sessions with: SELECT pid, xact_start, state, query FROM pg_stat_activity WHERE state IN ('idle in transaction','idle in transaction (aborted)'). " +
				"Terminate stale sessions with pg_terminate_backend(pid). " +
				"Consider setting idle_in_transaction_session_timeout (e.g. '5min') to auto-terminate such sessions.",
			DocRef: docIdleInTransactionTimeout,
		}
		return &a
	}
	a := pgtuner.Advice{
		Level:   pgtuner.Info,
		Section: workloadSection,
		Title:   fmt.Sprintf("Idle-in-transaction sessions detected (%d)", count),
		Message: fmt.Sprintf(
			"This is a point-in-time snapshot: %d session(s) are in state 'idle in transaction' or "+
				"'idle in transaction (aborted)'; "+
				"the oldest has been idle-in-transaction for %.0f seconds. "+
				"These sessions hold row-level locks and can block autovacuum if they remain open.",
			count, oldestSecs),
		Detail: "Consider setting idle_in_transaction_session_timeout to automatically terminate sessions that remain idle-in-transaction beyond a threshold. " +
			"Review with: SELECT pid, xact_start, state FROM pg_stat_activity WHERE state IN ('idle in transaction','idle in transaction (aborted)').",
		DocRef: docIdleInTransactionTimeout,
	}
	return &a
}

// evalLongRunningQueries is the pure decision function for long-running active
// query advice.  It reports an Info when there are active client-backend queries
// older than the threshold, noting the count and oldest age.  Long-running
// queries can indicate missing indexes, table bloat, or runaway operations.
//
//   - count > 0 AND oldest > 300 s → Info (report count and oldest age)
//   - else                         → nil
//
// Note: results reflect a point-in-time snapshot of pg_stat_activity.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
func evalLongRunningQueries(count int64, oldestSecs float64) *pgtuner.Advice {
	if count <= 0 || oldestSecs <= longRunningQueryOldestInfoSecs {
		return nil
	}
	a := pgtuner.Advice{
		Level:   pgtuner.Info,
		Section: workloadSection,
		Title:   fmt.Sprintf("Long-running active queries detected (%d)", count),
		Message: fmt.Sprintf(
			"This is a point-in-time snapshot: %d active client-backend query(ies) have been running for more than %.0f seconds; "+
				"the longest has been running for %.0f seconds (%.1f minutes). "+
				"Long-running queries may indicate missing indexes, lock contention, or very large data scans.",
			count, longRunningQueryOldestInfoSecs, oldestSecs, oldestSecs/60),
		Detail: "Review active queries with: SELECT pid, query_start, now()-query_start AS age, state, query FROM pg_stat_activity WHERE state='active'. " +
			"Consider pg_cancel_backend(pid) for queries that should not still be running. " +
			"Use EXPLAIN (ANALYZE, BUFFERS) to investigate query plans.",
		DocRef: docPGStatActivity,
	}
	return &a
}

// evalBlockedSessions is the pure decision function for lock-wait advice.
// Any session waiting on a Lock event means at least one query is blocked by
// another, which can cascade and stall the application.
//
//   - count > 0 → Warn
//   - else      → nil
//
// Note: results reflect a point-in-time snapshot of pg_stat_activity.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
// src: https://www.postgresql.org/docs/current/functions-info.html#FUNCTIONS-PG-BLOCKING-PIDS
func evalBlockedSessions(count int64) *pgtuner.Advice {
	if count <= 0 {
		return nil
	}
	a := pgtuner.Advice{
		Level:   pgtuner.Warn,
		Section: workloadSection,
		Title:   fmt.Sprintf("Blocked sessions / lock waits detected (%d)", count),
		Message: fmt.Sprintf(
			"This is a point-in-time snapshot: %d session(s) are waiting for a lock (wait_event_type='Lock' or pg_blocking_pids(pid) is non-empty). "+
				"Lock waits stall queries and, if they persist, can cascade into connection exhaustion.",
			count),
		Detail: "Identify blocking sessions with: " +
			"SELECT pid, pg_blocking_pids(pid) AS blocked_by, wait_event_type, wait_event, query FROM pg_stat_activity WHERE cardinality(pg_blocking_pids(pid)) > 0. " +
			"Terminate the blocker with pg_terminate_backend(blocking_pid) after identifying it. " +
			"Long-term, review locking patterns and consider shorter transactions.",
		DocRef: docPGBlockingPids,
	}
	return &a
}

// evalDanglingPreparedXacts is the pure decision function for dangling prepared
// transaction advice.  Prepared transactions that are not committed or rolled
// back hold locks on the rows they touched, consume transaction IDs (advancing
// wraparound risk), and block autovacuum from cleaning those rows.
//
//   - count > 0 → Warn (report count and oldest age)
//   - else      → nil
//
// Note: results reflect a point-in-time snapshot of pg_prepared_xacts.
// src: https://www.postgresql.org/docs/current/view-pg-prepared-xacts.html
func evalDanglingPreparedXacts(count int64, oldestSecs float64) *pgtuner.Advice {
	if count <= 0 {
		return nil
	}
	a := pgtuner.Advice{
		Level:   pgtuner.Warn,
		Section: workloadSection,
		Title:   fmt.Sprintf("Dangling prepared transactions detected (%d)", count),
		Message: fmt.Sprintf(
			"This is a point-in-time snapshot: pg_prepared_xacts shows %d prepared-but-not-committed/rolled-back transaction(s); "+
				"the oldest has been prepared for %.0f seconds (%.1f minutes). "+
				"Prepared transactions hold row-level locks, retain transaction IDs (advancing wraparound risk), "+
				"and prevent autovacuum from reclaiming dead tuples on rows they have touched.",
			count, oldestSecs, oldestSecs/60),
		Detail: "List dangling prepared transactions with: SELECT gid, prepared, owner, database FROM pg_prepared_xacts. " +
			"Commit or roll back each with COMMIT PREPARED '<gid>' or ROLLBACK PREPARED '<gid>'. " +
			"Ensure the two-phase commit coordinator is functioning correctly.",
		DocRef: docPGPreparedXacts,
	}
	return &a
}

// ----- WorkloadCheck ---------------------------------------------------------

// WorkloadCheck evaluates workload and connection health from live activity
// views: connection utilization, idle-in-transaction sessions, long-running
// queries, lock waits, and dangling prepared transactions.
// All sub-checks are independent; a query failure returns Unknown for that
// sub-check while siblings continue.  It never panics and never returns an error.
type WorkloadCheck struct{ databaseScoped }

func (WorkloadCheck) Name() string { return "workload" }

// Run executes all workload sub-checks.  Each sub-check is independent; a query
// failure produces a single Unknown-level advice for that sub-check while
// siblings continue.  All results are point-in-time snapshots of the activity
// views at the moment the check runs.
func (WorkloadCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	var out []pgtuner.Advice

	out = append(out, runConnUtilization(ctx, p)...)
	out = append(out, runIdleInTransaction(ctx, p)...)
	out = append(out, runLongRunningQueries(ctx, p)...)
	out = append(out, runBlockedSessions(ctx, p)...)
	out = append(out, runDanglingPreparedXacts(ctx, p)...)

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.OK,
			Section: workloadSection,
			Title:   "Workload looks healthy",
			Message: "Point-in-time snapshot: connection utilization is below 60%; no idle-in-transaction sessions, " +
				"long-running queries, lock waits, or dangling prepared transactions detected.",
			DocRef: docPGStatActivity,
		})
	}
	return out
}

// runConnUtilization queries pg_stat_activity for the count of client backends
// and compares against max_connections from the Settings snapshot.
// A failure to read max_connections from settings yields Unknown advice.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
// src: https://www.postgresql.org/docs/current/runtime-config-connection.html#GUC-MAX-CONNECTIONS
func runConnUtilization(ctx context.Context, p *Probe) []pgtuner.Advice {
	// max_connections comes from the cached pg_settings snapshot.
	// src: https://www.postgresql.org/docs/current/runtime-config-connection.html#GUC-MAX-CONNECTIONS
	maxConns, ok := p.Settings.Int("max_connections")
	if !ok || maxConns <= 0 {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: workloadSection,
			Title:   "Connection utilization check skipped: max_connections unavailable",
			Message: "Could not read max_connections from pg_settings; connection utilization advice is unavailable.",
			DocRef:  docPGStatActivity,
		}}
	}

	// Count only client backends; walsender, autovacuum, background worker, etc.
	// are not application connections and should not count toward the threshold.
	// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
	const sql = `SELECT count(*) FROM pg_stat_activity WHERE backend_type = 'client backend'`
	var used int64
	if err := p.Conn.QueryRow(ctx, sql).Scan(&used); err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: workloadSection,
			Title:   "Connection utilization query failed",
			Message: fmt.Sprintf("Could not query pg_stat_activity for client backend count: %v", err),
			DocRef:  docPGStatActivity,
		}}
	}

	if a := evalConnUtilization(used, maxConns); a != nil {
		return []pgtuner.Advice{*a}
	}
	return nil
}

// runIdleInTransaction queries pg_stat_activity for sessions in the
// 'idle in transaction' or 'idle in transaction (aborted)' state, returning
// count and the oldest xact_start age.  Both states hold row-level locks and
// block autovacuum on any tables they have touched; the aborted variant also
// cannot commit and must be rolled back.
// A COALESCE ensures a zero value when no such sessions exist.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
// src: https://www.postgresql.org/docs/current/runtime-config-client.html#GUC-IDLE-IN-TRANSACTION-SESSION-TIMEOUT
func runIdleInTransaction(ctx context.Context, p *Probe) []pgtuner.Advice {
	// 'idle in transaction' identifies sessions that have started a transaction
	// (BEGIN) and are between commands; they hold all locks acquired during the
	// transaction.  'idle in transaction (aborted)' is the same but an error has
	// occurred since the last command; the transaction cannot commit.  Both states
	// are equally harmful for lock retention and vacuum.
	// COALESCE(EXTRACT(EPOCH FROM max(now()-xact_start)), 0) gives the age of the
	// oldest such session in seconds, or 0 when there are none.
	// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
	const sql = `
SELECT count(*),
       COALESCE(EXTRACT(EPOCH FROM max(now() - xact_start)), 0)
FROM pg_stat_activity
WHERE state IN ('idle in transaction', 'idle in transaction (aborted)')`

	var count int64
	var oldestSecs float64
	if err := p.Conn.QueryRow(ctx, sql).Scan(&count, &oldestSecs); err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: workloadSection,
			Title:   "Idle-in-transaction query failed",
			Message: fmt.Sprintf("Could not query pg_stat_activity for idle-in-transaction sessions: %v", err),
			DocRef:  docPGStatActivity,
		}}
	}

	if a := evalIdleInTransaction(count, oldestSecs); a != nil {
		return []pgtuner.Advice{*a}
	}
	return nil
}

// runLongRunningQueries queries pg_stat_activity for active client-backend
// queries, excluding self-referential queries against pg_stat_activity.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
func runLongRunningQueries(ctx context.Context, p *Probe) []pgtuner.Advice {
	// We count only client backends in state='active' and exclude our own query
	// (which would appear as active since we're querying pg_stat_activity right now)
	// by filtering out queries that reference pg_stat_activity.
	// query_start is the time the current command started; COALESCE gives 0 when
	// there are no matching sessions.
	// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
	const sql = `
SELECT count(*),
       COALESCE(EXTRACT(EPOCH FROM max(now() - query_start)), 0)
FROM pg_stat_activity
WHERE state = 'active'
  AND backend_type = 'client backend'
  AND query NOT ILIKE '%pg_stat_activity%'`

	var count int64
	var oldestSecs float64
	if err := p.Conn.QueryRow(ctx, sql).Scan(&count, &oldestSecs); err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: workloadSection,
			Title:   "Long-running query check failed",
			Message: fmt.Sprintf("Could not query pg_stat_activity for long-running queries: %v", err),
			DocRef:  docPGStatActivity,
		}}
	}

	if a := evalLongRunningQueries(count, oldestSecs); a != nil {
		return []pgtuner.Advice{*a}
	}
	return nil
}

// runBlockedSessions queries pg_stat_activity for sessions that are blocked
// waiting for a lock, using pg_blocking_pids() to identify waiters whose
// blocker list is non-empty.  cardinality() returns the array length; > 0 means
// at least one blocking PID exists.
// pg_blocking_pids() is available from PG9.6+; all supported PG versions qualify.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
// src: https://www.postgresql.org/docs/current/functions-info.html#FUNCTIONS-PG-BLOCKING-PIDS
func runBlockedSessions(ctx context.Context, p *Probe) []pgtuner.Advice {
	// cardinality(pg_blocking_pids(pid)) > 0 identifies sessions that have at
	// least one blocker.  This is more precise than wait_event_type='Lock' alone
	// because it uses the lock dependency graph maintained by PostgreSQL.
	// Verified live: cardinality(pg_blocking_pids(pid)) works on PG 9.6+.
	// src: https://www.postgresql.org/docs/current/functions-info.html#FUNCTIONS-PG-BLOCKING-PIDS
	const sql = `SELECT count(*) FROM pg_stat_activity WHERE cardinality(pg_blocking_pids(pid)) > 0`

	var count int64
	if err := p.Conn.QueryRow(ctx, sql).Scan(&count); err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: workloadSection,
			Title:   "Blocked sessions query failed",
			Message: fmt.Sprintf("Could not query pg_stat_activity for blocked sessions via pg_blocking_pids(): %v", err),
			DocRef:  docPGBlockingPids,
		}}
	}

	if a := evalBlockedSessions(count); a != nil {
		return []pgtuner.Advice{*a}
	}
	return nil
}

// runDanglingPreparedXacts queries pg_prepared_xacts for transactions that have
// been prepared (via PREPARE TRANSACTION) but not yet committed or rolled back.
// src: https://www.postgresql.org/docs/current/view-pg-prepared-xacts.html
func runDanglingPreparedXacts(ctx context.Context, p *Probe) []pgtuner.Advice {
	// pg_prepared_xacts shows all two-phase commit transactions that are currently
	// in the prepared state.  COALESCE(EXTRACT(EPOCH FROM max(now()-prepared)), 0)
	// gives the age of the oldest dangling transaction in seconds, or 0 when none.
	// src: https://www.postgresql.org/docs/current/view-pg-prepared-xacts.html
	const sql = `
SELECT count(*),
       COALESCE(EXTRACT(EPOCH FROM max(now() - prepared)), 0)
FROM pg_prepared_xacts`

	var count int64
	var oldestSecs float64
	if err := p.Conn.QueryRow(ctx, sql).Scan(&count, &oldestSecs); err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: workloadSection,
			Title:   "Prepared transactions query failed",
			Message: fmt.Sprintf("Could not query pg_prepared_xacts for dangling prepared transactions: %v", err),
			DocRef:  docPGPreparedXacts,
		}}
	}

	if a := evalDanglingPreparedXacts(count, oldestSecs); a != nil {
		return []pgtuner.Advice{*a}
	}
	return nil
}
