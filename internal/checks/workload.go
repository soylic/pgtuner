package checks

import (
	"context"
	"fmt"

	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
const docPGStatActivity = "https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW"

// src: https://www.postgresql.org/docs/current/runtime-config-client.html#GUC-IDLE-IN-TRANSACTION-SESSION-TIMEOUT
const docIdleInTransactionTimeout = "https://www.postgresql.org/docs/current/runtime-config-client.html#GUC-IDLE-IN-TRANSACTION-SESSION-TIMEOUT"

// src: https://www.postgresql.org/docs/current/functions-info.html#FUNCTIONS-PG-BLOCKING-PIDS
const docPGBlockingPids = "https://www.postgresql.org/docs/current/functions-info.html#FUNCTIONS-PG-BLOCKING-PIDS"

// src: https://www.postgresql.org/docs/current/view-pg-prepared-xacts.html
const docPGPreparedXacts = "https://www.postgresql.org/docs/current/view-pg-prepared-xacts.html"

const workloadSection = "Workload"

// connUtilizationWarnThreshold is the max_connections fraction above which we Warn.
// Exhausting max_connections causes FATAL "sorry, too many clients"; 80% is an early warning.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
// src: https://www.postgresql.org/docs/current/runtime-config-connection.html#GUC-MAX-CONNECTIONS
const connUtilizationWarnThreshold = 0.80

// connUtilizationInfoThreshold is the max_connections fraction above which we
// recommend a connection pooler.
// src: https://www.postgresql.org/docs/current/runtime-config-connection.html#GUC-MAX-CONNECTIONS
const connUtilizationInfoThreshold = 0.60

// idleInTransactionOldestWarnSecs is the oldest-session age above which advice
// becomes Warn. Past 5 minutes a session is almost certainly stale.
// src: https://www.postgresql.org/docs/current/runtime-config-client.html#GUC-IDLE-IN-TRANSACTION-SESSION-TIMEOUT
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
const idleInTransactionOldestWarnSecs = 300.0

// longRunningQueryOldestInfoSecs is the oldest active query age above which we
// emit an Info. It is a soft threshold.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
const longRunningQueryOldestInfoSecs = 300.0

// evalConnUtilization returns at most one Advice from the client backend count
// (used) and max_connections (max). It reflects a point-in-time snapshot.
//
//   - used/max > 80% → Warn
//   - used/max > 60% → Info
//   - else           → nil
//
// src: https://www.postgresql.org/docs/current/runtime-config-connection.html#GUC-MAX-CONNECTIONS
func evalConnUtilization(used, max int64) *pgtuner.Advice {
	if max <= 0 {
		return nil
	}
	ratio := float64(used) / float64(max)
	switch {
	case ratio > connUtilizationWarnThreshold:
		a := pgtuner.Advice{
			ID:      "workload.connections-high",
			Level:   pgtuner.Warn,
			Section: workloadSection,
			Title:   "Connection utilization is high (>80%)",
			Message: fmt.Sprintf(
				"This is a point-in-time snapshot: client backends use %d of %d max_connections (%.1f%%). "+
					"At this level, PostgreSQL may reject new connections with \"sorry, too many clients\" under burst load.",
				used, max, ratio*100),
			Detail: "Consider deploying a connection pooler (for example PgBouncer) to multiplex application connections. " +
				"Alternatively, reduce long-lived idle connections. If RAM allows, increase max_connections.",
			DocRef: docPGStatActivity,
		}
		return &a
	case ratio > connUtilizationInfoThreshold:
		a := pgtuner.Advice{
			ID:      "workload.connections-moderate",
			Level:   pgtuner.Info,
			Section: workloadSection,
			Title:   "Connection utilization is moderate (>60%)",
			Message: fmt.Sprintf(
				"This is a point-in-time snapshot: client backends use %d of %d max_connections (%.1f%%). "+
					"Use a connection pooler before utilization reaches the 80%% warning level.",
				used, max, ratio*100),
			Detail: "A connection pooler (for example PgBouncer in transaction mode) can serve many more application connections " +
				"than backends and use far fewer server-side processes.",
			DocRef: docPGStatActivity,
		}
		return &a
	default:
		return nil
	}
}

// evalIdleInTransaction returns idle-in-transaction advice. Both 'idle in
// transaction' and its '(aborted)' variant hold row-level locks and block
// autovacuum. The aborted variant also cannot commit and needs a rollback.
//
//   - count > 0 AND oldest > 300 s → Warn
//   - count > 0 (younger)          → Info
//   - count == 0                   → nil
//
// src: https://www.postgresql.org/docs/current/runtime-config-client.html#GUC-IDLE-IN-TRANSACTION-SESSION-TIMEOUT
func evalIdleInTransaction(count int64, oldestSecs float64) *pgtuner.Advice {
	if count <= 0 {
		return nil
	}
	if oldestSecs > idleInTransactionOldestWarnSecs {
		a := pgtuner.Advice{
			ID:      "workload.idle-in-transaction-long",
			Level:   pgtuner.Warn,
			Section: workloadSection,
			Title:   fmt.Sprintf("Idle-in-transaction sessions detected (%d): oldest >5 min", count),
			Message: fmt.Sprintf(
				"This is a point-in-time snapshot: %d session(s) are in state 'idle in transaction' or "+
					"'idle in transaction (aborted)'. "+
					"The oldest has been idle-in-transaction for %.0f seconds (%.1f minutes). "+
					"These sessions hold row-level locks. They prevent autovacuum from reclaiming dead tuples on the tables they touched.",
				count, oldestSecs, oldestSecs/60),
			Detail: "Identify the sessions with: SELECT pid, xact_start, state, query FROM pg_stat_activity WHERE state IN ('idle in transaction','idle in transaction (aborted)'). " +
				"Terminate stale sessions with pg_terminate_backend(pid). " +
				"Consider setting idle_in_transaction_session_timeout (for example '5min') to terminate such sessions automatically.",
			DocRef: docIdleInTransactionTimeout,
		}
		return &a
	}
	a := pgtuner.Advice{
		ID:      "workload.idle-in-transaction",
		Level:   pgtuner.Info,
		Section: workloadSection,
		Title:   fmt.Sprintf("Idle-in-transaction sessions detected (%d)", count),
		Message: fmt.Sprintf(
			"This is a point-in-time snapshot: %d session(s) are in state 'idle in transaction' or "+
				"'idle in transaction (aborted)'. "+
				"The oldest has been idle-in-transaction for %.0f seconds. "+
				"These sessions hold row-level locks. They can block autovacuum if they remain open.",
			count, oldestSecs),
		Detail: "Consider setting idle_in_transaction_session_timeout to terminate sessions automatically when they remain idle-in-transaction beyond a threshold. " +
			"Review them with: SELECT pid, xact_start, state FROM pg_stat_activity WHERE state IN ('idle in transaction','idle in transaction (aborted)').",
		DocRef: docIdleInTransactionTimeout,
	}
	return &a
}

// evalLongRunningQueries returns an Info when active client-backend queries
// are older than the threshold.
//
//   - count > 0 AND oldest > 300 s → Info
//   - else                         → nil
//
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
func evalLongRunningQueries(count int64, oldestSecs float64) *pgtuner.Advice {
	if count <= 0 || oldestSecs <= longRunningQueryOldestInfoSecs {
		return nil
	}
	a := pgtuner.Advice{
		ID:      "workload.long-running-queries",
		Level:   pgtuner.Info,
		Section: workloadSection,
		Title:   fmt.Sprintf("Long-running active queries detected (%d)", count),
		Message: fmt.Sprintf(
			"This is a point-in-time snapshot: %d active client-backend query(ies) have been running for more than %.0f seconds. "+
				"The longest has been running for %.0f seconds (%.1f minutes). "+
				"Long-running queries may indicate missing indexes, lock contention, or very large data scans.",
			count, longRunningQueryOldestInfoSecs, oldestSecs, oldestSecs/60),
		Detail: "Review active queries with: SELECT pid, query_start, now()-query_start AS age, state, query FROM pg_stat_activity WHERE state='active'. " +
			"Consider pg_cancel_backend(pid) for queries that must not run any longer. " +
			"Use EXPLAIN (ANALYZE, BUFFERS) to examine query plans.",
		DocRef: docPGStatActivity,
	}
	return &a
}

// evalBlockedSessions returns a Warn when any session waits on a lock. One
// blocked query can cascade and stall the application.
// src: https://www.postgresql.org/docs/current/functions-info.html#FUNCTIONS-PG-BLOCKING-PIDS
func evalBlockedSessions(count int64) *pgtuner.Advice {
	if count <= 0 {
		return nil
	}
	a := pgtuner.Advice{
		ID:      "workload.blocked-sessions",
		Level:   pgtuner.Warn,
		Section: workloadSection,
		Title:   fmt.Sprintf("Blocked sessions and lock waits detected (%d)", count),
		Message: fmt.Sprintf(
			"This is a point-in-time snapshot: %d session(s) are waiting for a lock (wait_event_type='Lock' or pg_blocking_pids(pid) is non-empty). "+
				"Lock waits stall queries. If they persist, they can cascade into connection exhaustion.",
			count),
		Detail: "Identify the blocking sessions with: " +
			"SELECT pid, pg_blocking_pids(pid) AS blocked_by, wait_event_type, wait_event, query FROM pg_stat_activity WHERE cardinality(pg_blocking_pids(pid)) > 0. " +
			"Then terminate the blocker with pg_terminate_backend(blocking_pid). " +
			"For the long term, review the locking patterns and consider shorter transactions.",
		DocRef: docPGBlockingPids,
	}
	return &a
}

// evalDanglingPreparedXacts returns a Warn when prepared transactions remain
// uncommitted. They hold row locks, retain transaction IDs, and block autovacuum.
// src: https://www.postgresql.org/docs/current/view-pg-prepared-xacts.html
func evalDanglingPreparedXacts(count int64, oldestSecs float64) *pgtuner.Advice {
	if count <= 0 {
		return nil
	}
	a := pgtuner.Advice{
		ID:      "workload.prepared-xacts-dangling",
		Level:   pgtuner.Warn,
		Section: workloadSection,
		Title:   fmt.Sprintf("Dangling prepared transactions detected (%d)", count),
		Message: fmt.Sprintf(
			"This is a point-in-time snapshot: pg_prepared_xacts shows %d prepared transaction(s) that nobody committed or rolled back. "+
				"The oldest has been prepared for %.0f seconds (%.1f minutes). "+
				"Prepared transactions hold row-level locks and retain transaction IDs (this advances wraparound risk). "+
				"They also prevent autovacuum from reclaiming dead tuples on the rows they touched.",
			count, oldestSecs, oldestSecs/60),
		Detail: "List dangling prepared transactions with: SELECT gid, prepared, owner, database FROM pg_prepared_xacts. " +
			"Commit or roll back each one with COMMIT PREPARED '<gid>' or ROLLBACK PREPARED '<gid>'. " +
			"Make sure the two-phase commit coordinator works correctly.",
		DocRef: docPGPreparedXacts,
	}
	return &a
}

// WorkloadCheck evaluates connection and workload health from live activity
// views. Sub-checks are independent: a query failure yields Unknown for that
// sub-check and the others continue.
type WorkloadCheck struct{ databaseScoped }

func (WorkloadCheck) Name() string { return "workload" }

func (WorkloadCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	var out []pgtuner.Advice

	out = append(out, runConnUtilization(ctx, p)...)
	out = append(out, runIdleInTransaction(ctx, p)...)
	out = append(out, runLongRunningQueries(ctx, p)...)
	out = append(out, runBlockedSessions(ctx, p)...)
	out = append(out, runDanglingPreparedXacts(ctx, p)...)

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			ID:      "workload.ok",
			Level:   pgtuner.OK,
			Section: workloadSection,
			Title:   "Workload looks healthy",
			Message: "Point-in-time snapshot: connection utilization is below 60%. " +
				"No idle-in-transaction sessions, long-running queries, lock waits, or dangling prepared transactions detected.",
			DocRef: docPGStatActivity,
		})
	}
	return out
}

// runConnUtilization compares the client backend count with max_connections from
// the Settings snapshot. A missing max_connections yields Unknown advice.
func runConnUtilization(ctx context.Context, p *Probe) []pgtuner.Advice {
	maxConns, ok := p.Settings.Int("max_connections")
	if !ok || maxConns <= 0 {
		return []pgtuner.Advice{{
			ID:      "workload.max-connections-unavailable",
			Level:   pgtuner.Unknown,
			Section: workloadSection,
			Title:   "Connection utilization check skipped: max_connections unavailable",
			Message: "Could not read max_connections from pg_settings. Connection utilization advice is unavailable.",
			DocRef:  docPGStatActivity,
		}}
	}

	// Count only client backends. Walsender, autovacuum, and background workers are not application connections.
	const sql = `SELECT count(*) FROM pg_stat_activity WHERE backend_type = 'client backend'`
	var used int64
	if err := p.Conn.QueryRow(ctx, sql).Scan(&used); err != nil {
		return []pgtuner.Advice{{
			ID:      "workload.connections-query-failed",
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

// runIdleInTransaction returns the count of idle-in-transaction sessions and the
// age of the oldest xact_start. COALESCE gives 0 when none exist.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
func runIdleInTransaction(ctx context.Context, p *Probe) []pgtuner.Advice {
	// The aborted state means an error occurred since the last command, so the
	// transaction cannot commit. Both states retain locks and block vacuum equally.
	const sql = `
SELECT count(*),
       COALESCE(EXTRACT(EPOCH FROM max(now() - xact_start)), 0)
FROM pg_stat_activity
WHERE state IN ('idle in transaction', 'idle in transaction (aborted)')`

	var count int64
	var oldestSecs float64
	if err := p.Conn.QueryRow(ctx, sql).Scan(&count, &oldestSecs); err != nil {
		return []pgtuner.Advice{{
			ID:      "workload.idle-in-transaction-query-failed",
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

// runLongRunningQueries reads active client-backend queries from pg_stat_activity.
func runLongRunningQueries(ctx context.Context, p *Probe) []pgtuner.Advice {
	// The filter on pg_stat_activity excludes our own query, which appears as active.
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
			ID:      "workload.long-running-queries-query-failed",
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

// runBlockedSessions counts sessions whose pg_blocking_pids() list is non-empty.
// pg_blocking_pids() exists since PG9.6, so all supported versions qualify.
func runBlockedSessions(ctx context.Context, p *Probe) []pgtuner.Advice {
	// More precise than wait_event_type='Lock' alone because it uses PostgreSQL's lock dependency graph.
	// src: https://www.postgresql.org/docs/current/functions-info.html#FUNCTIONS-PG-BLOCKING-PIDS
	const sql = `SELECT count(*) FROM pg_stat_activity WHERE cardinality(pg_blocking_pids(pid)) > 0`

	var count int64
	if err := p.Conn.QueryRow(ctx, sql).Scan(&count); err != nil {
		return []pgtuner.Advice{{
			ID:      "workload.blocked-sessions-query-failed",
			Level:   pgtuner.Unknown,
			Section: workloadSection,
			Title:   "Blocked sessions query failed",
			Message: fmt.Sprintf("Could not query pg_stat_activity for blocked sessions with pg_blocking_pids(): %v", err),
			DocRef:  docPGBlockingPids,
		}}
	}

	if a := evalBlockedSessions(count); a != nil {
		return []pgtuner.Advice{*a}
	}
	return nil
}

// runDanglingPreparedXacts counts transactions prepared with PREPARE TRANSACTION
// and not yet committed or rolled back.
func runDanglingPreparedXacts(ctx context.Context, p *Probe) []pgtuner.Advice {
	const sql = `
SELECT count(*),
       COALESCE(EXTRACT(EPOCH FROM max(now() - prepared)), 0)
FROM pg_prepared_xacts`

	var count int64
	var oldestSecs float64
	if err := p.Conn.QueryRow(ctx, sql).Scan(&count, &oldestSecs); err != nil {
		return []pgtuner.Advice{{
			ID:      "workload.prepared-xacts-query-failed",
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
