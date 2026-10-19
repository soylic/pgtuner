package checks

import (
	"context"
	"fmt"
	"strings"

	"github.com/soylic/pgtuner/internal/pg"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// src: https://www.postgresql.org/docs/current/view-pg-replication-slots.html
const docReplicationSlots = "https://www.postgresql.org/docs/current/view-pg-replication-slots.html"

// src: https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-MAX-WAL-SENDERS
const docWALSenders = "https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-MAX-WAL-SENDERS"

// docStatReplicationSlots documents pg_stat_replication_slots (PG14+).
// src: https://www.postgresql.org/docs/14/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-SLOTS-VIEW
const docStatReplicationSlots = "https://www.postgresql.org/docs/14/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-SLOTS-VIEW"

// src: https://www.postgresql.org/docs/current/hot-standby.html
const docHotStandby = "https://www.postgresql.org/docs/current/hot-standby.html"

// src: https://www.postgresql.org/docs/current/runtime-config-replication.html
const docRuntimeReplication = "https://www.postgresql.org/docs/current/runtime-config-replication.html"

// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-VIEW
const docStatReplication = "https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-VIEW"

// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-WAL-RECEIVER-VIEW
const docWalReceiver = "https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-WAL-RECEIVER-VIEW"

// src: https://www.postgresql.org/docs/current/logical-replication.html
const docLogicalReplication = "https://www.postgresql.org/docs/current/logical-replication.html"

// src: https://www.postgresql.org/docs/current/runtime-config-wal.html#GUC-WAL-LEVEL
const docRuntimeWAL = "https://www.postgresql.org/docs/current/runtime-config-wal.html#GUC-WAL-LEVEL"

// replicationSlotSpillWarnThreshold is the minimum spill_bytes per slot that triggers an Info.
// Spilling means the consumer lags the WAL sender. 1 GiB avoids noise from briefly slow consumers.
// src: https://www.postgresql.org/docs/14/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-SLOTS-VIEW
const replicationSlotSpillWarnThreshold int64 = 1 * 1024 * 1024 * 1024 // 1 GiB

// replicationLagWarnSeconds is the replay lag above which a standby is far behind.
// Shorter lags are normal burst behaviour.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-VIEW
const replicationLagWarnSeconds = 30.0

// evalInactiveSlots returns advice for inactive slots, which retain WAL indefinitely.
// src: https://www.postgresql.org/docs/current/view-pg-replication-slots.html
func evalInactiveSlots(names []string) *pgtuner.Advice {
	if len(names) == 0 {
		return nil
	}
	a := pgtuner.Advice{
		ID:      "replication.slot-inactive",
		Level:   pgtuner.Warn,
		Section: "Replication",
		Title:   "Inactive replication slot(s) retain WAL and can fill the disk",
		Message: fmt.Sprintf("pg_replication_slots shows %d inactive slot(s): %s. PostgreSQL retains all WAL segments produced since an inactive slot was last consumed. This can fill the disk.", len(names), strings.Join(names, ", ")),
		Detail:  "Drop unused slots with SELECT pg_drop_replication_slot('<name>'). Alternatively, resume the consumer. Monitor the pg_replication_slots.confirmed_flush_lsn lag.",
		DocRef:  docReplicationSlots,
	}
	return &a
}

// evalRoleInfo returns the Info advice for the detected server role.
// src: https://www.postgresql.org/docs/current/functions-admin.html (pg_is_in_recovery)
// src: https://www.postgresql.org/docs/current/monitoring-stats.html (pg_stat_replication)
func evalRoleInfo(r pg.Role) pgtuner.Advice {
	const section = "Replication"
	switch r.Role {
	case pg.RolePrimary:
		return pgtuner.Advice{
			ID:      "replication.role-primary",
			Level:   pgtuner.Info,
			Section: section,
			Title:   "This server is a PRIMARY",
			Message: fmt.Sprintf("pg_is_in_recovery() returned false. pg_stat_replication shows %d connected standby(s). This server is the replication source.", r.ConnectedStandbys),
			DocRef:  docStatReplication,
		}
	case pg.RoleStandby:
		return pgtuner.Advice{
			ID:      "replication.role-standby",
			Level:   pgtuner.Info,
			Section: section,
			Title:   "This server is a STANDBY (in recovery)",
			Message: "pg_is_in_recovery() returned true. This server is a streaming standby or an archive-recovery standby.",
			DocRef:  docHotStandby,
		}
	case pg.RoleStandalone:
		return pgtuner.Advice{
			ID:      "replication.role-standalone",
			Level:   pgtuner.Info,
			Section: section,
			Title:   "This server is standalone (no replication)",
			Message: "pg_is_in_recovery() returned false. pg_stat_replication shows 0 connected standbys. Replication is not in use now.",
			DocRef:  docWALSenders,
		}
	default:
		return pgtuner.Advice{
			ID:      "replication.role-unknown",
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Replication role could not be determined",
			Message: "DetectRole failed. Replication-role advice is not available.",
		}
	}
}

// maxStandbyDelayHighThresholdMs is the max_standby_streaming_delay (and max_standby_archive_delay) above which the delay is "very high".
// 5 minutes is a generous bound. Most installations use 30 s or less.
// src: https://www.postgresql.org/docs/current/runtime-config-replication.html
const maxStandbyDelayHighThresholdMs int64 = 300_000

// evalStandbyArchiveDelay returns advice for max_standby_archive_delay. A delay of 0 cancels conflicting queries immediately.
// src: https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-MAX-STANDBY-ARCHIVE-DELAY
// src: https://www.postgresql.org/docs/current/hot-standby.html
func evalStandbyArchiveDelay(rawVal string) []pgtuner.Advice {
	const section = "Replication"
	if rawVal == "0" {
		return []pgtuner.Advice{{
			ID:      "replication.standby-archive-delay-zero",
			Level:   pgtuner.Info,
			Section: section,
			Title:   "max_standby_archive_delay=0: queries cancelled immediately during archive recovery conflicts",
			Message: "max_standby_archive_delay=0 means the standby cancels immediately any query that conflicts with WAL application from the archive. There is no grace period. This gives the highest replay throughput but lowers query reliability.",
			Detail:  "Consider a positive max_standby_archive_delay (for example '30s'). Running queries then have time to complete before the standby cancels them.",
			DocRef:  docRuntimeReplication,
		}}
	}
	return nil
}

// evalStandbyStreamingDelayHigh returns advice when max_standby_streaming_delay is -1 or above the threshold.
// src: https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-MAX-STANDBY-STREAMING-DELAY
// src: https://www.postgresql.org/docs/current/hot-standby.html
func evalStandbyStreamingDelayHigh(delayMs int64) []pgtuner.Advice {
	const section = "Replication"
	if delayMs == -1 || delayMs > maxStandbyDelayHighThresholdMs {
		label := fmt.Sprintf("%d ms", delayMs)
		if delayMs == -1 {
			label = "-1 (wait forever)"
		}
		return []pgtuner.Advice{{
			ID:      "replication.standby-streaming-delay-high",
			Level:   pgtuner.Info,
			Section: section,
			Title:   "max_standby_streaming_delay is very high: long conflict waits can cause primary bloat",
			Message: fmt.Sprintf("max_standby_streaming_delay=%s. When a WAL replay record conflicts with a running query, the standby waits this long before it cancels the query. Very long waits stall WAL application. This increases replication lag and makes the primary retain dead rows (bloat) for longer.", label),
			Detail:  "Consider a shorter delay (for example 30 000 ms = 30s). If long queries must run reliably on the standby without primary bloat, also set hot_standby_feedback=on.",
			DocRef:  docRuntimeReplication,
		}}
	}
	return nil
}

// evalHotStandbyFeedbackOn returns an Info advice when hot_standby_feedback is on.
// The setting prevents cancellations but can increase primary bloat.
// src: https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-HOT-STANDBY-FEEDBACK
// src: https://www.postgresql.org/docs/current/hot-standby.html
func evalHotStandbyFeedbackOn(feedbackOn bool) []pgtuner.Advice {
	const section = "Replication"
	if feedbackOn {
		return []pgtuner.Advice{{
			ID:      "replication.hot-standby-feedback-on",
			Level:   pgtuner.Info,
			Section: section,
			Title:   "hot_standby_feedback=on: prevents query cancellations but can increase primary bloat",
			Message: "hot_standby_feedback=on makes this standby report its oldest active transaction to the primary. The primary does not vacuum rows that standby queries still need. This prevents cancellations. The tradeoff is that the primary may accumulate dead rows (bloat) if standby queries run for a long time.",
			Detail:  "This is an intentional tradeoff between standby query stability and primary table bloat. When hot_standby_feedback is on, monitor the primary for excessive dead tuples (pg_stat_user_tables.n_dead_tup).",
			DocRef:  docRuntimeReplication,
		}}
	}
	return nil
}

// ReplicaRow holds per-standby lag data from pg_stat_replication.
// Lag is in seconds. A nil value means the column was NULL (the standby has not started replay).
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-VIEW
type ReplicaRow struct {
	ApplicationName string
	SyncState       string
	ReplayLagSecs   *float64 // nullable; EXTRACT(EPOCH FROM replay_lag)
}

// evalReplicationLag returns Warn advice for any standby with replay_lag over 30 s.
// It also warns when synchronous_standby_names is set but no standby is in sync state.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-VIEW
func evalReplicationLag(rows []ReplicaRow, syncStandbyNames string) []pgtuner.Advice {
	const section = "Replication"
	var out []pgtuner.Advice

	for _, r := range rows {
		if r.ReplayLagSecs != nil && *r.ReplayLagSecs > replicationLagWarnSeconds {
			out = append(out, pgtuner.Advice{
				ID:      "replication.standby-replay-lag",
				Level:   pgtuner.Warn,
				Section: section,
				Title:   fmt.Sprintf("Standby %q has significant replay lag", r.ApplicationName),
				Message: fmt.Sprintf("Standby %q (sync_state=%q) has a replay_lag of %.1f seconds. This exceeds the %g-second threshold. The standby lags behind the primary.", r.ApplicationName, r.SyncState, *r.ReplayLagSecs, replicationLagWarnSeconds),
				Detail:  "Check the network bandwidth and the disk I/O on the standby. Check also if the standby has a heavy load. Review pg_stat_replication.replay_lag.",
				DocRef:  docStatReplication,
			})
		}
	}

	// Without a sync standby, commits wait indefinitely or fall back to asynchronous, depending on synchronous_commit.
	// src: https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-SYNCHRONOUS-STANDBY-NAMES
	if strings.TrimSpace(syncStandbyNames) != "" {
		hasSyncStandby := false
		for _, r := range rows {
			if r.SyncState == "sync" {
				hasSyncStandby = true
				break
			}
		}
		if !hasSyncStandby {
			out = append(out, pgtuner.Advice{
				ID:      "replication.sync-standby-missing",
				Level:   pgtuner.Warn,
				Section: section,
				Title:   "synchronous_standby_names is set but no standby is in sync state",
				Message: fmt.Sprintf("synchronous_standby_names=%q but pg_stat_replication shows no standby with sync_state='sync'. Synchronous commit may not give the expected durability guarantee.", syncStandbyNames),
				Detail:  "Make sure the named standbys are connected and streaming. Review pg_stat_replication.sync_state and synchronous_commit.",
				DocRef:  docRuntimeReplication,
			})
		}
	}

	return out
}

// evalWalLevelVsLogical returns Bad advice when logical slots or publications exist but wal_level is not 'logical'.
// src: https://www.postgresql.org/docs/current/runtime-config-wal.html#GUC-WAL-LEVEL
func evalWalLevelVsLogical(walLevel string, logicalSlots, pubCount int) *pgtuner.Advice {
	if logicalSlots == 0 && pubCount == 0 {
		return nil
	}
	if walLevel == "logical" {
		return nil
	}
	a := pgtuner.Advice{
		ID:      "replication.wal-level-not-logical",
		Level:   pgtuner.Bad,
		Section: "Replication",
		Title:   "wal_level must be 'logical' for logical replication to function",
		Message: fmt.Sprintf("wal_level=%q but there are %d logical replication slot(s) and/or %d publication(s). Logical replication requires wal_level='logical'. The current setting is not sufficient.", walLevel, logicalSlots, pubCount),
		Detail:  "Set wal_level=logical in postgresql.conf and reload. A server restart is required.",
		DocRef:  docRuntimeWAL,
	}
	return &a
}

// ReplicationCheck evaluates WAL sender capacity and replication slot health.
type ReplicationCheck struct{ clusterScoped }

func (ReplicationCheck) Name() string { return "replication" }

// Run checks replication settings and queries live slot metadata.
// A query failure gives one Unknown advice for that sub-check and does not block the others.
func (ReplicationCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "Replication"
	var out []pgtuner.Advice

	out = append(out, evalRoleInfo(p.Role))

	// max_wal_senders=0 disables streaming replication and pg_basebackup. It can be intentional on a non-primary node.
	// src: https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-MAX-WAL-SENDERS
	if mws, ok := p.Settings.Int("max_wal_senders"); ok && mws == 0 {
		out = append(out, pgtuner.Advice{
			ID:      "replication.max-wal-senders-zero",
			Level:   pgtuner.Info,
			Section: section,
			Title:   "max_wal_senders=0; no streaming replication or WAL-streaming backup possible",
			Message: "max_wal_senders=0 disables all WAL-sender processes. Streaming replication standby servers and pg_basebackup cannot connect. This may be intentional on a standalone server.",
			Detail:  "To support standby servers and streaming backups, set max_wal_senders to at least 2.",
			DocRef:  docWALSenders,
		})
	}

	if p.Role.Role == pg.RoleStandby {
		out = append(out, runStandbyChecks(ctx, p)...)
	}

	if p.Role.Role == pg.RolePrimary {
		out = append(out, runPrimaryChecks(ctx, p)...)
	}

	// src: https://www.postgresql.org/docs/current/view-pg-replication-slots.html
	const inactiveSlotsSQL = "SELECT slot_name FROM pg_replication_slots WHERE NOT active ORDER BY slot_name"
	slotRows, err := p.Conn.Query(ctx, inactiveSlotsSQL)
	if err != nil {
		out = append(out, pgtuner.Advice{
			ID:      "replication.slot-query-failed",
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Replication slot query failed",
			Message: fmt.Sprintf("pgtuner could not query pg_replication_slots for inactive slots: %v", err),
		})
	} else {
		func() {
			defer slotRows.Close()
			var names []string
			for slotRows.Next() {
				var name string
				if err := slotRows.Scan(&name); err != nil {
					out = append(out, pgtuner.Advice{
						ID:      "replication.slot-scan-failed",
						Level:   pgtuner.Unknown,
						Section: section,
						Title:   "Replication slot scan failed",
						Message: fmt.Sprintf("pgtuner could not scan a pg_replication_slots row: %v", err),
					})
					return
				}
				names = append(names, name)
			}
			if err := slotRows.Err(); err != nil {
				out = append(out, pgtuner.Advice{
					ID:      "replication.slot-iteration-failed",
					Level:   pgtuner.Unknown,
					Section: section,
					Title:   "Replication slot iteration failed",
					Message: fmt.Sprintf("pgtuner got an error while it read the pg_replication_slots rows: %v", err),
				})
				return
			}
			if a := evalInactiveSlots(names); a != nil {
				out = append(out, *a)
			}
		}()
	}

	// PG14+ only. Large spill_bytes means the logical slot consumer is far behind.
	// src: https://www.postgresql.org/docs/14/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-SLOTS-VIEW
	if p.Version.AtLeast(14, 0) {
		const spillSQL = `SELECT slot_name, spill_bytes FROM pg_stat_replication_slots
			WHERE spill_bytes > $1 ORDER BY spill_bytes DESC LIMIT 5`
		spillRows, err := p.Conn.Query(ctx, spillSQL, replicationSlotSpillWarnThreshold)
		if err != nil {
			out = append(out, pgtuner.Advice{
				ID:      "replication.slot-spill-stats-unavailable",
				Level:   pgtuner.Unknown,
				Section: section,
				Title:   "Replication slot spill stats unavailable",
				Message: fmt.Sprintf("pgtuner could not query pg_stat_replication_slots for spill statistics: %v", err),
			})
		} else {
			func() {
				defer spillRows.Close()
				for spillRows.Next() {
					var slotName string
					var spillBytes int64
					if err := spillRows.Scan(&slotName, &spillBytes); err != nil {
						out = append(out, pgtuner.Advice{
							ID:      "replication.slot-spill-scan-failed",
							Level:   pgtuner.Unknown,
							Section: section,
							Title:   "Replication spill scan failed",
							Message: fmt.Sprintf("pgtuner could not scan a pg_stat_replication_slots row: %v", err),
						})
						return
					}
					out = append(out, pgtuner.Advice{
						ID:      "replication.slot-spill-large",
						Level:   pgtuner.Info,
						Section: section,
						Title:   fmt.Sprintf("Replication slot %q has large WAL spill (PG14+)", slotName),
						Message: fmt.Sprintf("Slot %q has spilled %.1f GiB of decoded WAL to disk. Large spill values indicate that the logical replication consumer lags far behind the WAL stream.", slotName, float64(spillBytes)/float64(1<<30)),
						Detail:  "Check the consumer process (subscriber or logical replication client). Consider a higher wal_sender_timeout. Review the consumer throughput.",
						DocRef:  docStatReplicationSlots,
					})
				}
				if err := spillRows.Err(); err != nil {
					out = append(out, pgtuner.Advice{
						ID:      "replication.slot-spill-stats-query-failed",
						Level:   pgtuner.Unknown,
						Section: section,
						Title:   "Replication spill stats unavailable",
						Message: fmt.Sprintf("pgtuner got an error while it read pg_stat_replication_slots: %v", err),
					})
				}
			}()
		}
	}

	out = append(out, runLogicalTypeChecks(ctx, p)...)

	return out
}

// runStandbyChecks emits advice for a server in recovery. Query failures give Unknown advice.
// src: https://www.postgresql.org/docs/current/hot-standby.html
// src: https://www.postgresql.org/docs/current/runtime-config-replication.html
func runStandbyChecks(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "Replication"
	var out []pgtuner.Advice

	// src: https://www.postgresql.org/docs/current/hot-standby.html
	if !p.Settings.Bool("hot_standby") {
		out = append(out, pgtuner.Advice{
			ID:      "replication.hot-standby-off",
			Level:   pgtuner.Info,
			Section: section,
			Title:   "hot_standby=off: read queries cannot run on this standby",
			Message: "hot_standby is disabled. This standby server cannot serve read-only SQL queries from clients.",
			Detail:  "To allow read-only queries on the standby, set hot_standby=on. This requires a server restart.",
			DocRef:  docHotStandby,
		})
	}

	// With feedback off, vacuum on the primary may cancel long queries on this standby.
	// src: https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-HOT-STANDBY-FEEDBACK
	if !p.Settings.Bool("hot_standby_feedback") {
		out = append(out, pgtuner.Advice{
			ID:      "replication.hot-standby-feedback-off",
			Level:   pgtuner.Info,
			Section: section,
			Title:   "hot_standby_feedback=off: standby queries may be cancelled by primary vacuum",
			Message: "hot_standby_feedback=off means the standby does not tell the primary which rows it needs. Aggressive VACUUM on the primary can cancel long-running queries on this standby.",
			Detail:  "Set hot_standby_feedback=on to prevent cancellations from vacuum conflicts. This trades query stability for slightly more table bloat on the primary.",
			DocRef:  docRuntimeReplication,
		})
	}

	// src: https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-MAX-STANDBY-ARCHIVE-DELAY
	if v, ok := p.Settings.Get("max_standby_archive_delay"); ok {
		out = append(out, evalStandbyArchiveDelay(v)...)
	}

	// src: https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-MAX-STANDBY-STREAMING-DELAY
	if v, ok := p.Settings.Get("max_standby_streaming_delay"); ok {
		if v == "0" {
			out = append(out, pgtuner.Advice{
				ID:      "replication.standby-streaming-delay-zero",
				Level:   pgtuner.Info,
				Section: section,
				Title:   "max_standby_streaming_delay=0: standby queries cancelled immediately on conflict",
				Message: "max_standby_streaming_delay=0 means the standby cancels immediately any query that conflicts with WAL replay. There is no grace period.",
				Detail:  "Consider a positive max_standby_streaming_delay (for example '30s'). Running queries then have time to finish before the standby cancels them.",
				DocRef:  docRuntimeReplication,
			})
		} else if msd, ok2 := p.Settings.Int("max_standby_streaming_delay"); ok2 {
			out = append(out, evalStandbyStreamingDelayHigh(msd)...)
		}
	}

	// src: https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-HOT-STANDBY-FEEDBACK
	// src: https://www.postgresql.org/docs/current/hot-standby.html
	out = append(out, evalHotStandbyFeedbackOn(p.Settings.Bool("hot_standby_feedback"))...)

	// No row in pg_stat_wal_receiver means the standby is not streaming.
	// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-WAL-RECEIVER-VIEW
	const walReceiverSQL = "SELECT status FROM pg_stat_wal_receiver LIMIT 1"
	var status string
	err := p.Conn.QueryRow(ctx, walReceiverSQL).Scan(&status)
	if err != nil {
		// Any error other than no rows is a query failure.
		if strings.Contains(err.Error(), "no rows") {
			out = append(out, pgtuner.Advice{
				ID:      "replication.standby-receiver-missing",
				Level:   pgtuner.Info,
				Section: section,
				Title:   "Standby has no active streaming receiver (pg_stat_wal_receiver is empty)",
				Message: "pg_stat_wal_receiver has no rows. This standby may use archive recovery instead of streaming replication. Streaming may also be disconnected for a short time.",
				DocRef:  docWalReceiver,
			})
		} else {
			out = append(out, pgtuner.Advice{
				ID:      "replication.wal-receiver-query-failed",
				Level:   pgtuner.Unknown,
				Section: section,
				Title:   "pg_stat_wal_receiver query failed",
				Message: fmt.Sprintf("pgtuner could not query pg_stat_wal_receiver: %v", err),
				DocRef:  docWalReceiver,
			})
		}
	}

	return out
}

// runPrimaryChecks emits advice for a primary with connected standbys. Query failures give Unknown advice.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-VIEW
func runPrimaryChecks(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "Replication"
	var out []pgtuner.Advice

	// EXTRACT(EPOCH FROM replay_lag) returns NULL when replay_lag is NULL.
	// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-VIEW
	const lagSQL = `SELECT application_name, sync_state,
		EXTRACT(EPOCH FROM replay_lag)::float8
		FROM pg_stat_replication
		ORDER BY application_name`

	rows, err := p.Conn.Query(ctx, lagSQL)
	if err != nil {
		out = append(out, pgtuner.Advice{
			ID:      "replication.lag-query-failed",
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "pg_stat_replication lag query failed",
			Message: fmt.Sprintf("pgtuner could not query pg_stat_replication for standby lag: %v", err),
			DocRef:  docStatReplication,
		})
	} else {
		func() {
			defer rows.Close()
			var replicaRows []ReplicaRow
			for rows.Next() {
				var appName, syncState string
				var lagSecs *float64
				if err := rows.Scan(&appName, &syncState, &lagSecs); err != nil {
					out = append(out, pgtuner.Advice{
						ID:      "replication.lag-scan-failed",
						Level:   pgtuner.Unknown,
						Section: section,
						Title:   "pg_stat_replication lag scan failed",
						Message: fmt.Sprintf("pgtuner could not scan a pg_stat_replication row: %v", err),
						DocRef:  docStatReplication,
					})
					return
				}
				replicaRows = append(replicaRows, ReplicaRow{
					ApplicationName: appName,
					SyncState:       syncState,
					ReplayLagSecs:   lagSecs,
				})
			}
			if err := rows.Err(); err != nil {
				out = append(out, pgtuner.Advice{
					ID:      "replication.lag-iteration-failed",
					Level:   pgtuner.Unknown,
					Section: section,
					Title:   "pg_stat_replication iteration failed",
					Message: fmt.Sprintf("pgtuner got an error while it read pg_stat_replication: %v", err),
					DocRef:  docStatReplication,
				})
				return
			}
			syncNames, _ := p.Settings.Get("synchronous_standby_names")
			out = append(out, evalReplicationLag(replicaRows, syncNames)...)
		}()
	}

	// With zero WAL retention and no slots, a standby may desync when WAL is recycled.
	// PG13+ uses wal_keep_size (in MiB). PG12 and earlier use wal_keep_segments.
	// src: https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-WAL-KEEP-SIZE
	// src: https://www.postgresql.org/docs/12/runtime-config-replication.html#GUC-WAL-KEEP-SEGMENTS
	walKeeperIsZero := false
	if p.Version.AtLeast(13, 0) {
		if v, ok := p.Settings.Int("wal_keep_size"); ok && v == 0 {
			walKeeperIsZero = true
		}
	} else {
		if v, ok := p.Settings.Int("wal_keep_segments"); ok && v == 0 {
			walKeeperIsZero = true
		}
	}

	if walKeeperIsZero {
		var slotCount int
		if err := p.Conn.QueryRow(ctx, "SELECT count(*) FROM pg_replication_slots").Scan(&slotCount); err != nil {
			out = append(out, pgtuner.Advice{
				ID:      "replication.wal-keep-slot-count-failed",
				Level:   pgtuner.Unknown,
				Section: section,
				Title:   "Could not count replication slots for WAL-keep check",
				Message: fmt.Sprintf("pgtuner could not count pg_replication_slots: %v", err),
			})
		} else if slotCount == 0 {
			walKeepGUC := "wal_keep_size"
			if !p.Version.AtLeast(13, 0) {
				walKeepGUC = "wal_keep_segments"
			}
			out = append(out, pgtuner.Advice{
				ID:      "replication.wal-keep-zero-no-slots",
				Level:   pgtuner.Warn,
				Section: section,
				Title:   fmt.Sprintf("%s=0 with no replication slots: standbys may desync if WAL is recycled", walKeepGUC),
				Message: fmt.Sprintf("%s=0 and there are no replication slots. If a standby disconnects, PostgreSQL may recycle the WAL segments that the standby needs to resume streaming. The standby then needs a full re-sync.", walKeepGUC),
				Detail:  fmt.Sprintf("Set %s to retain enough WAL for standbys to reconnect after short outages. Alternatively, create a replication slot to retain WAL automatically.", walKeepGUC),
				DocRef:  docRuntimeReplication,
			})
		}
	}

	return out
}

// runLogicalTypeChecks emits advice about logical replication. Query failures give Unknown advice.
// src: https://www.postgresql.org/docs/current/logical-replication.html
// src: https://www.postgresql.org/docs/current/view-pg-replication-slots.html
// src: https://www.postgresql.org/docs/current/catalog-pg-publication.html
// src: https://www.postgresql.org/docs/current/catalog-pg-subscription.html
func runLogicalTypeChecks(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "Replication"
	var out []pgtuner.Advice

	var logicalSlotCount int
	if err := p.Conn.QueryRow(ctx,
		"SELECT count(*) FROM pg_replication_slots WHERE slot_type = 'logical'",
	).Scan(&logicalSlotCount); err != nil {
		out = append(out, pgtuner.Advice{
			ID:      "replication.logical-slot-count-failed",
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Could not count logical replication slots",
			Message: fmt.Sprintf("pgtuner could not query pg_replication_slots for logical slots: %v", err),
			DocRef:  docReplicationSlots,
		})
		logicalSlotCount = -1 // sentinel for a failed query
	} else if logicalSlotCount > 0 {
		out = append(out, pgtuner.Advice{
			ID:      "replication.logical-decoding-in-use",
			Level:   pgtuner.Info,
			Section: section,
			Title:   fmt.Sprintf("Logical decoding is in use (%d logical slot(s))", logicalSlotCount),
			Message: fmt.Sprintf("pg_replication_slots shows %d logical replication slot(s). Logical decoding is active on this server.", logicalSlotCount),
			Detail:  "Make sure the logical replication consumers (subscribers, CDC tools) are active. Inactive logical slots retain WAL indefinitely.",
			DocRef:  docReplicationSlots,
		})
	}

	// src: https://www.postgresql.org/docs/current/catalog-pg-publication.html
	var pubCount int
	if err := p.Conn.QueryRow(ctx, "SELECT count(*) FROM pg_publication").Scan(&pubCount); err != nil {
		out = append(out, pgtuner.Advice{
			ID:      "replication.publication-count-failed",
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Could not count publications",
			Message: fmt.Sprintf("pgtuner could not query pg_publication: %v", err),
			DocRef:  docLogicalReplication,
		})
		pubCount = -1 // sentinel for a failed query
	} else if pubCount > 0 {
		out = append(out, pgtuner.Advice{
			ID:      "replication.publisher-active",
			Level:   pgtuner.Info,
			Section: section,
			Title:   fmt.Sprintf("Logical replication publisher: %d publication(s) defined", pubCount),
			Message: fmt.Sprintf("pg_publication shows %d publication(s). This server publishes data through logical replication.", pubCount),
			DocRef:  docLogicalReplication,
		})
	}

	// src: https://www.postgresql.org/docs/current/catalog-pg-subscription.html
	var subCount int
	if err := p.Conn.QueryRow(ctx, "SELECT count(*) FROM pg_subscription").Scan(&subCount); err != nil {
		out = append(out, pgtuner.Advice{
			ID:      "replication.subscription-count-failed",
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Could not count subscriptions",
			Message: fmt.Sprintf("pgtuner could not query pg_subscription: %v", err),
			DocRef:  docLogicalReplication,
		})
		subCount = -1 // sentinel for a failed query
	} else if subCount > 0 {
		out = append(out, pgtuner.Advice{
			ID:      "replication.subscriber-active",
			Level:   pgtuner.Info,
			Section: section,
			Title:   fmt.Sprintf("Logical replication subscriber: %d subscription(s) active", subCount),
			Message: fmt.Sprintf("pg_subscription shows %d subscription(s). This server subscribes to logical replication from one or more publishers.", subCount),
			DocRef:  docLogicalReplication,
		})
		// Best effort: count active apply workers (available since PG10).
		// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-SUBSCRIPTION-VIEW
		var activeWorkers int
		if err := p.Conn.QueryRow(ctx,
			"SELECT count(*) FROM pg_stat_subscription WHERE pid IS NOT NULL",
		).Scan(&activeWorkers); err != nil {
			out = append(out, pgtuner.Advice{
				ID:      "replication.subscription-workers-query-failed",
				Level:   pgtuner.Unknown,
				Section: section,
				Title:   "Could not query pg_stat_subscription apply workers",
				Message: fmt.Sprintf("pgtuner could not query pg_stat_subscription: %v", err),
				DocRef:  docLogicalReplication,
			})
		}
	}

	// Run only when both counts are known (no -1 sentinel).
	// src: https://www.postgresql.org/docs/current/runtime-config-wal.html#GUC-WAL-LEVEL
	if logicalSlotCount >= 0 && pubCount >= 0 {
		walLevel, _ := p.Settings.Get("wal_level")
		if a := evalWalLevelVsLogical(walLevel, logicalSlotCount, pubCount); a != nil {
			out = append(out, *a)
		}
	}

	return out
}
