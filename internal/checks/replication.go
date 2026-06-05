package checks

import (
	"context"
	"fmt"
	"strings"

	"github.com/OpenRemedy/pgtuner/internal/pg"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// docReplicationSlots is the canonical reference for pg_replication_slots.
// src: https://www.postgresql.org/docs/current/view-pg-replication-slots.html
const docReplicationSlots = "https://www.postgresql.org/docs/current/view-pg-replication-slots.html"

// docWALSenders is the canonical reference for max_wal_senders.
// src: https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-MAX-WAL-SENDERS
const docWALSenders = "https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-MAX-WAL-SENDERS"

// docStatReplicationSlots is the reference for pg_stat_replication_slots (PG14+).
// src: https://www.postgresql.org/docs/14/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-SLOTS-VIEW
const docStatReplicationSlots = "https://www.postgresql.org/docs/14/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-SLOTS-VIEW"

// docHotStandby is the reference for hot standby configuration.
// src: https://www.postgresql.org/docs/current/hot-standby.html
const docHotStandby = "https://www.postgresql.org/docs/current/hot-standby.html"

// docRuntimeReplication is the reference for runtime replication GUCs.
// src: https://www.postgresql.org/docs/current/runtime-config-replication.html
const docRuntimeReplication = "https://www.postgresql.org/docs/current/runtime-config-replication.html"

// docStatReplication is the reference for pg_stat_replication.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-VIEW
const docStatReplication = "https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-VIEW"

// docWalReceiver is the reference for pg_stat_wal_receiver.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-WAL-RECEIVER-VIEW
const docWalReceiver = "https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-WAL-RECEIVER-VIEW"

// docLogicalReplication is the reference for logical replication.
// src: https://www.postgresql.org/docs/current/logical-replication.html
const docLogicalReplication = "https://www.postgresql.org/docs/current/logical-replication.html"

// docRuntimeWAL is the reference for wal_level GUC.
// src: https://www.postgresql.org/docs/current/runtime-config-wal.html#GUC-WAL-LEVEL
const docRuntimeWAL = "https://www.postgresql.org/docs/current/runtime-config-wal.html#GUC-WAL-LEVEL"

// replicationSlotSpillWarnThreshold is the minimum spill_bytes per slot that
// triggers an Info notice. A slot spilling to disk means its consumer is lagging
// significantly behind the WAL sender, causing on-disk WAL accumulation. We use
// 1 GiB as a non-trivial threshold to avoid noise on briefly slow consumers.
// src: https://www.postgresql.org/docs/14/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-SLOTS-VIEW
const replicationSlotSpillWarnThreshold int64 = 1 * 1024 * 1024 * 1024 // 1 GiB

// replicationLagWarnThreshold is the replay-lag duration in seconds above which
// a standby is considered significantly behind. 30 seconds is a conservative
// threshold; shorter lags are normal burst behaviour.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-VIEW
const replicationLagWarnSeconds = 30.0

// evalInactiveSlots is the pure decision function for inactive replication slot
// advice. Inactive slots retain WAL segments indefinitely, potentially filling
// the disk.
// src: https://www.postgresql.org/docs/current/view-pg-replication-slots.html
func evalInactiveSlots(names []string) *pgtuner.Advice {
	if len(names) == 0 {
		return nil
	}
	a := pgtuner.Advice{
		Level:   pgtuner.Warn,
		Section: "Replication",
		Title:   "Inactive replication slot(s) retain WAL and can fill the disk",
		Message: fmt.Sprintf("pg_replication_slots shows %d inactive slot(s): %s. An inactive slot causes PostgreSQL to retain all WAL segments produced since the slot was last consumed, potentially filling the disk.", len(names), strings.Join(names, ", ")),
		Detail:  "Drop unused slots with SELECT pg_drop_replication_slot('<name>'), or resume the consumer. Monitor pg_replication_slots.confirmed_flush_lsn lag.",
		DocRef:  docReplicationSlots,
	}
	return &a
}

// evalRoleInfo produces the Info advice announcing the detected server role.
// It is a pure function taking a pg.Role value.
// src: https://www.postgresql.org/docs/current/functions-admin.html (pg_is_in_recovery)
// src: https://www.postgresql.org/docs/current/monitoring-stats.html (pg_stat_replication)
func evalRoleInfo(r pg.Role) pgtuner.Advice {
	const section = "Replication"
	switch r.Role {
	case pg.RolePrimary:
		return pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "This server is a PRIMARY",
			Message: fmt.Sprintf("pg_is_in_recovery() returned false and pg_stat_replication shows %d connected standby(s). This server is the replication source.", r.ConnectedStandbys),
			DocRef:  docStatReplication,
		}
	case pg.RoleStandby:
		return pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "This server is a STANDBY (in recovery)",
			Message: "pg_is_in_recovery() returned true. This server is a streaming or archive-recovery standby.",
			DocRef:  docHotStandby,
		}
	case pg.RoleStandalone:
		return pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "This server is standalone (no replication)",
			Message: "pg_is_in_recovery() returned false and pg_stat_replication shows 0 connected standbys. Replication is not currently in use.",
			DocRef:  docWALSenders,
		}
	default:
		return pgtuner.Advice{
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Replication role could not be determined",
			Message: "DetectRole failed; replication-role advice is unavailable.",
		}
	}
}

// maxStandbyDelayHighThresholdMs is the max_standby_streaming_delay (and
// max_standby_archive_delay) value in milliseconds above which we consider the
// delay "very high" and warn about bloat risk on the primary.  5 minutes is a
// generous upper bound; most installations use ≤ 30 s.
// src: https://www.postgresql.org/docs/current/runtime-config-replication.html
const maxStandbyDelayHighThresholdMs int64 = 300_000 // 5 minutes in ms

// evalStandbyArchiveDelay is the pure decision function for
// max_standby_archive_delay standby checks.  A delay of 0 means immediate
// cancellation; -1 or > threshold means "wait too long".
// src: https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-MAX-STANDBY-ARCHIVE-DELAY
// src: https://www.postgresql.org/docs/current/hot-standby.html
func evalStandbyArchiveDelay(rawVal string) []pgtuner.Advice {
	const section = "Replication"
	if rawVal == "0" {
		return []pgtuner.Advice{{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "max_standby_archive_delay=0: queries cancelled immediately during archive recovery conflicts",
			Message: "max_standby_archive_delay=0 means that any query on this standby conflicting with WAL application from the archive is cancelled immediately, without any grace period. This maximises replay throughput at the cost of query reliability.",
			Detail:  "Consider setting max_standby_archive_delay to a positive value (e.g. '30s') to allow running queries a chance to complete before being cancelled.",
			DocRef:  docRuntimeReplication,
		}}
	}
	return nil
}

// evalStandbyStreamingDelay is the pure decision function for
// max_standby_streaming_delay standby checks.  A delay of -1 or > threshold
// means "wait too long", causing primary bloat.
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
			Level:   pgtuner.Info,
			Section: section,
			Title:   "max_standby_streaming_delay is very high: long conflict waits can cause primary bloat",
			Message: fmt.Sprintf("max_standby_streaming_delay=%s. When a WAL replay record conflicts with a running query, the standby will wait this long before cancelling the query. Very long waits stall WAL application, increasing replication lag and forcing the primary to retain dead rows (bloat) for longer.", label),
			Detail:  "Consider a shorter delay (e.g. 30 000 ms = 30s). Pair with hot_standby_feedback=on if long queries must run reliably on the standby without causing primary bloat.",
			DocRef:  docRuntimeReplication,
		}}
	}
	return nil
}

// evalHotStandbyFeedbackOn is the pure decision function for
// hot_standby_feedback=on.  Enabling feedback prevents cancellations at the
// cost of increased primary bloat; both states deserve an Info notice.
// src: https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-HOT-STANDBY-FEEDBACK
// src: https://www.postgresql.org/docs/current/hot-standby.html
func evalHotStandbyFeedbackOn(feedbackOn bool) []pgtuner.Advice {
	const section = "Replication"
	if feedbackOn {
		return []pgtuner.Advice{{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "hot_standby_feedback=on: prevents query cancellations but can increase primary bloat",
			Message: "hot_standby_feedback=on causes this standby to report its oldest active transaction to the primary. The primary will not vacuum rows still needed by standby queries, preventing cancellations. The tradeoff is that the primary may accumulate dead rows (bloat) if standby queries are long-running.",
			Detail:  "This is an intentional tradeoff between standby query stability and primary table bloat. Monitor the primary for excessive dead tuples (pg_stat_user_tables.n_dead_tup) when hot_standby_feedback is enabled.",
			DocRef:  docRuntimeReplication,
		}}
	}
	return nil
}

// ReplicaRow holds per-standby lag data from pg_stat_replication.
// Lag values are in seconds extracted from the interval columns; nil means
// the column was NULL (standby hasn't started replaying yet).
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-VIEW
type ReplicaRow struct {
	ApplicationName string
	SyncState       string
	ReplayLagSecs   *float64 // nullable; EXTRACT(EPOCH FROM replay_lag)
}

// evalReplicationLag is the pure decision function for primary-side lag advice.
// It inspects a slice of ReplicaRow values and returns advice for:
//   - any standby with replay_lag > 30 s → Warn
//   - synchronous_standby_names is set but no standby is in sync state → Warn
//
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-VIEW
func evalReplicationLag(rows []ReplicaRow, syncStandbyNames string) []pgtuner.Advice {
	const section = "Replication"
	var out []pgtuner.Advice

	for _, r := range rows {
		if r.ReplayLagSecs != nil && *r.ReplayLagSecs > replicationLagWarnSeconds {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Warn,
				Section: section,
				Title:   fmt.Sprintf("Standby %q has significant replay lag", r.ApplicationName),
				Message: fmt.Sprintf("Standby %q (sync_state=%q) has a replay_lag of %.1f seconds, which exceeds the %g-second threshold. The standby is lagging behind the primary.", r.ApplicationName, r.SyncState, *r.ReplayLagSecs, replicationLagWarnSeconds),
				Detail:  "Investigate network bandwidth, disk I/O on the standby, or whether the standby is under heavy load. Review pg_stat_replication.replay_lag.",
				DocRef:  docStatReplication,
			})
		}
	}

	// synchronous_standby_names is set but no standby is in 'sync' state.
	// This means synchronous commits are either waiting indefinitely or falling
	// back to asynchronous depending on synchronous_commit setting.
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
				Level:   pgtuner.Warn,
				Section: section,
				Title:   "synchronous_standby_names is set but no standby is in sync state",
				Message: fmt.Sprintf("synchronous_standby_names=%q but pg_stat_replication shows no standby with sync_state='sync'. Synchronous commit may not be providing the expected durability guarantee.", syncStandbyNames),
				Detail:  "Check that the named standbys are connected and streaming. Review pg_stat_replication.sync_state and synchronous_commit.",
				DocRef:  docRuntimeReplication,
			})
		}
	}

	return out
}

// evalWalLevelVsLogical is the pure decision function for the wal_level-vs-logical check.
// If logical replication is in use (logical slots or publications exist) but
// wal_level is not 'logical', logical replication cannot function.
// src: https://www.postgresql.org/docs/current/runtime-config-wal.html#GUC-WAL-LEVEL
func evalWalLevelVsLogical(walLevel string, logicalSlots, pubCount int) *pgtuner.Advice {
	if logicalSlots == 0 && pubCount == 0 {
		return nil
	}
	if walLevel == "logical" {
		return nil
	}
	a := pgtuner.Advice{
		Level:   pgtuner.Bad,
		Section: "Replication",
		Title:   "wal_level must be 'logical' for logical replication to function",
		Message: fmt.Sprintf("wal_level=%q but there are %d logical replication slot(s) and/or %d publication(s). Logical replication requires wal_level='logical'; current setting is insufficient.", walLevel, logicalSlots, pubCount),
		Detail:  "Set wal_level=logical in postgresql.conf and reload. A server restart is required.",
		DocRef:  docRuntimeWAL,
	}
	return &a
}

// ReplicationCheck evaluates WAL sender capacity and replication slot health.
type ReplicationCheck struct{ clusterScoped }

func (ReplicationCheck) Name() string { return "replication" }

// Run checks replication-related settings and queries live slot metadata.
// Each sub-check is independent; a query failure produces a single Unknown
// advice for that sub-check and does not block the others.
// It never panics and never returns an error.
func (ReplicationCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "Replication"
	var out []pgtuner.Advice

	// --- Role report (Info) -------------------------------------------------------
	// Always emit the detected role as an informational baseline.
	out = append(out, evalRoleInfo(p.Role))

	// --- max_wal_senders (setting) ------------------------------------------------
	// max_wal_senders=0 disables all streaming replication and WAL-streaming
	// backups (pg_basebackup). This may be intentional on a non-primary node, but
	// worth surfacing.
	// src: https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-MAX-WAL-SENDERS
	if mws, ok := p.Settings.Int("max_wal_senders"); ok && mws == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "max_wal_senders=0; no streaming replication or WAL-streaming backup possible",
			Message: "max_wal_senders=0 disables all WAL-sender processes. Streaming replication standby servers and pg_basebackup cannot connect. This may be intentional on a standalone server.",
			Detail:  "Set max_wal_senders to at least 2 to support standby servers and streaming backups.",
			DocRef:  docWALSenders,
		})
	}

	// --- Standby-specific checks --------------------------------------------------
	// These only apply when this server is a streaming/archive standby.
	if p.Role.Role == pg.RoleStandby {
		out = append(out, runStandbyChecks(ctx, p)...)
	}

	// --- Primary-with-standbys checks ---------------------------------------------
	// These only apply when standbys are actively connected.
	if p.Role.Role == pg.RolePrimary {
		out = append(out, runPrimaryChecks(ctx, p)...)
	}

	// --- inactive slots (query) ---------------------------------------------------
	// Query pg_replication_slots for slots that are not currently active.
	// src: https://www.postgresql.org/docs/current/view-pg-replication-slots.html
	const inactiveSlotsSQL = "SELECT slot_name FROM pg_replication_slots WHERE NOT active ORDER BY slot_name"
	slotRows, err := p.Conn.Query(ctx, inactiveSlotsSQL)
	if err != nil {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Replication slot query failed",
			Message: fmt.Sprintf("Could not query pg_replication_slots for inactive slots: %v", err),
		})
	} else {
		func() {
			defer slotRows.Close()
			var names []string
			for slotRows.Next() {
				var name string
				if err := slotRows.Scan(&name); err != nil {
					out = append(out, pgtuner.Advice{
						Level:   pgtuner.Unknown,
						Section: section,
						Title:   "Replication slot scan failed",
						Message: fmt.Sprintf("Could not scan pg_replication_slots row: %v", err),
					})
					return
				}
				names = append(names, name)
			}
			if err := slotRows.Err(); err != nil {
				out = append(out, pgtuner.Advice{
					Level:   pgtuner.Unknown,
					Section: section,
					Title:   "Replication slot iteration failed",
					Message: fmt.Sprintf("Error iterating pg_replication_slots rows: %v", err),
				})
				return
			}
			if a := evalInactiveSlots(names); a != nil {
				out = append(out, *a)
			}
		}()
	}

	// --- pg_stat_replication_slots spill stats (PG≥14) ---------------------------
	// pg_stat_replication_slots tracks how much decoded WAL each logical replication
	// slot has spilled to disk. Large spill_bytes indicates the consumer is far
	// behind, stressing storage. We emit a light Info if any slot exceeds our
	// threshold; skip the query on older versions.
	// src: https://www.postgresql.org/docs/14/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-SLOTS-VIEW
	if p.Version.AtLeast(14, 0) {
		const spillSQL = `SELECT slot_name, spill_bytes FROM pg_stat_replication_slots
			WHERE spill_bytes > $1 ORDER BY spill_bytes DESC LIMIT 5`
		spillRows, err := p.Conn.Query(ctx, spillSQL, replicationSlotSpillWarnThreshold)
		if err != nil {
			// Non-fatal: spill stats are informational; don't block the rest.
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Unknown,
				Section: section,
				Title:   "Replication slot spill stats unavailable",
				Message: fmt.Sprintf("Could not query pg_stat_replication_slots for spill statistics: %v", err),
			})
		} else {
			func() {
				defer spillRows.Close()
				for spillRows.Next() {
					var slotName string
					var spillBytes int64
					if err := spillRows.Scan(&slotName, &spillBytes); err != nil {
						out = append(out, pgtuner.Advice{
							Level:   pgtuner.Unknown,
							Section: section,
							Title:   "Replication spill scan failed",
							Message: fmt.Sprintf("could not scan pg_stat_replication_slots row: %v", err),
						})
						return
					}
					out = append(out, pgtuner.Advice{
						Level:   pgtuner.Info,
						Section: section,
						Title:   fmt.Sprintf("Replication slot %q has large WAL spill (PG14+)", slotName),
						Message: fmt.Sprintf("Slot %q has spilled %.1f GiB of decoded WAL to disk. Large spill values indicate the logical replication consumer is lagging significantly behind the WAL stream.", slotName, float64(spillBytes)/float64(1<<30)),
						Detail:  "Check the consumer process (subscriber or logical replication client) and consider increasing wal_sender_timeout or reviewing consumer throughput.",
						DocRef:  docStatReplicationSlots,
					})
				}
				if err := spillRows.Err(); err != nil {
					out = append(out, pgtuner.Advice{
						Level:   pgtuner.Unknown,
						Section: section,
						Title:   "Replication spill stats unavailable",
						Message: fmt.Sprintf("error iterating pg_stat_replication_slots: %v", err),
					})
				}
			}()
		}
	}

	// --- Step 3: Logical replication type checks ----------------------------------
	out = append(out, runLogicalTypeChecks(ctx, p)...)

	// OK fallthrough: only the role-Info is emitted at this point if nothing else fired.
	// The role-Info always fires, so len(out) >= 1; no separate OK fallthrough needed.
	return out
}

// runStandbyChecks emits advice specific to a server that is in recovery (standby).
// Each sub-check is independent; query failures produce Unknown advice.
// src: https://www.postgresql.org/docs/current/hot-standby.html
// src: https://www.postgresql.org/docs/current/runtime-config-replication.html
func runStandbyChecks(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "Replication"
	var out []pgtuner.Advice

	// hot_standby: if off, read queries cannot run on this standby.
	// src: https://www.postgresql.org/docs/current/hot-standby.html
	if !p.Settings.Bool("hot_standby") {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "hot_standby=off: read queries cannot run on this standby",
			Message: "hot_standby is disabled. Read-only SQL queries from clients cannot be served by this standby server.",
			Detail:  "Set hot_standby=on to allow read-only queries on the standby. Requires a server restart.",
			DocRef:  docHotStandby,
		})
	}

	// hot_standby_feedback: if off, long-running queries on this standby may be
	// cancelled by vacuum activity on the primary.
	// src: https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-HOT-STANDBY-FEEDBACK
	if !p.Settings.Bool("hot_standby_feedback") {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "hot_standby_feedback=off: standby queries may be cancelled by primary vacuum",
			Message: "hot_standby_feedback=off means the standby does not inform the primary of rows it needs. Aggressive VACUUM on the primary can cancel long-running queries on this standby.",
			Detail:  "Enable hot_standby_feedback=on to prevent cancellations from vacuum conflicts. Note: this trades query stability for slightly increased table bloat on the primary.",
			DocRef:  docRuntimeReplication,
		})
	}

	// max_standby_archive_delay: 0 → immediate cancellation.
	// src: https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-MAX-STANDBY-ARCHIVE-DELAY
	if v, ok := p.Settings.Get("max_standby_archive_delay"); ok {
		out = append(out, evalStandbyArchiveDelay(v)...)
	}

	// max_standby_streaming_delay: 0 → immediate cancellation; very high → primary bloat risk.
	// src: https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-MAX-STANDBY-STREAMING-DELAY
	if v, ok := p.Settings.Get("max_standby_streaming_delay"); ok {
		if v == "0" {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Info,
				Section: section,
				Title:   "max_standby_streaming_delay=0: standby queries cancelled immediately on conflict",
				Message: "max_standby_streaming_delay=0 means that any query conflicting with WAL replay is cancelled immediately, without any grace period.",
				Detail:  "Consider setting max_standby_streaming_delay to a positive value (e.g. '30s') to give running queries a chance to finish before being cancelled.",
				DocRef:  docRuntimeReplication,
			})
		} else if msd, ok2 := p.Settings.Int("max_standby_streaming_delay"); ok2 {
			out = append(out, evalStandbyStreamingDelayHigh(msd)...)
		}
	}

	// hot_standby_feedback: off → vacuum-conflict cancellations; on → primary bloat tradeoff.
	// Both states are surfaced as Info.
	// src: https://www.postgresql.org/docs/current/runtime-config-replication.html#GUC-HOT-STANDBY-FEEDBACK
	// src: https://www.postgresql.org/docs/current/hot-standby.html
	out = append(out, evalHotStandbyFeedbackOn(p.Settings.Bool("hot_standby_feedback"))...)

	// pg_stat_wal_receiver: if no row, the standby is not currently streaming.
	// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-WAL-RECEIVER-VIEW
	const walReceiverSQL = "SELECT status FROM pg_stat_wal_receiver LIMIT 1"
	var status string
	err := p.Conn.QueryRow(ctx, walReceiverSQL).Scan(&status)
	if err != nil {
		// pgx returns pgx.ErrNoRows when the view is empty (no streaming receiver).
		// Any other error is a genuine query failure.
		if strings.Contains(err.Error(), "no rows") {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Info,
				Section: section,
				Title:   "Standby has no active streaming receiver (pg_stat_wal_receiver is empty)",
				Message: "pg_stat_wal_receiver has no rows. This standby may be using archive recovery rather than streaming replication, or streaming is temporarily disconnected.",
				DocRef:  docWalReceiver,
			})
		} else {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Unknown,
				Section: section,
				Title:   "pg_stat_wal_receiver query failed",
				Message: fmt.Sprintf("Could not query pg_stat_wal_receiver: %v", err),
				DocRef:  docWalReceiver,
			})
		}
	}
	// If scan succeeded: status is non-empty (e.g. "streaming") → no advice needed.

	return out
}

// runPrimaryChecks emits advice specific to a primary that has connected standbys.
// Each sub-check is independent; query failures produce Unknown advice.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-VIEW
func runPrimaryChecks(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "Replication"
	var out []pgtuner.Advice

	// Query per-standby lag from pg_stat_replication.
	// EXTRACT(EPOCH FROM replay_lag) returns NULL when replay_lag is NULL.
	// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-VIEW
	const lagSQL = `SELECT application_name, sync_state,
		EXTRACT(EPOCH FROM replay_lag)::float8
		FROM pg_stat_replication
		ORDER BY application_name`

	rows, err := p.Conn.Query(ctx, lagSQL)
	if err != nil {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "pg_stat_replication lag query failed",
			Message: fmt.Sprintf("Could not query pg_stat_replication for standby lag: %v", err),
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
						Level:   pgtuner.Unknown,
						Section: section,
						Title:   "pg_stat_replication lag scan failed",
						Message: fmt.Sprintf("Could not scan pg_stat_replication row: %v", err),
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
					Level:   pgtuner.Unknown,
					Section: section,
					Title:   "pg_stat_replication iteration failed",
					Message: fmt.Sprintf("Error iterating pg_stat_replication: %v", err),
					DocRef:  docStatReplication,
				})
				return
			}
			syncNames, _ := p.Settings.Get("synchronous_standby_names")
			out = append(out, evalReplicationLag(replicaRows, syncNames)...)
		}()
	}

	// wal_keep_size / wal_keep_segments: if zero and no replication slots, a standby
	// may desync if WAL is recycled between connects.
	// PG13+ uses wal_keep_size (in MiB); PG12 and earlier used wal_keep_segments.
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
		// Count replication slots as an alternative WAL-retention mechanism.
		var slotCount int
		if err := p.Conn.QueryRow(ctx, "SELECT count(*) FROM pg_replication_slots").Scan(&slotCount); err != nil {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Unknown,
				Section: section,
				Title:   "Could not count replication slots for WAL-keep check",
				Message: fmt.Sprintf("Could not count pg_replication_slots: %v", err),
			})
		} else if slotCount == 0 {
			walKeepGUC := "wal_keep_size"
			if !p.Version.AtLeast(13, 0) {
				walKeepGUC = "wal_keep_segments"
			}
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Warn,
				Section: section,
				Title:   fmt.Sprintf("%s=0 with no replication slots: standbys may desync if WAL is recycled", walKeepGUC),
				Message: fmt.Sprintf("%s=0 and there are no replication slots. If a standby disconnects, PostgreSQL may recycle the WAL segments it needs to resume streaming, forcing a full re-sync.", walKeepGUC),
				Detail:  fmt.Sprintf("Set %s to retain enough WAL for standbys to reconnect after brief outages, or create a replication slot to retain WAL automatically.", walKeepGUC),
				DocRef:  docRuntimeReplication,
			})
		}
	}

	return out
}

// runLogicalTypeChecks emits advice about logical replication (Step 3).
// Each sub-check is independent; query failures produce Unknown advice.
// src: https://www.postgresql.org/docs/current/logical-replication.html
// src: https://www.postgresql.org/docs/current/view-pg-replication-slots.html
// src: https://www.postgresql.org/docs/current/catalog-pg-publication.html
// src: https://www.postgresql.org/docs/current/catalog-pg-subscription.html
func runLogicalTypeChecks(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "Replication"
	var out []pgtuner.Advice

	// --- Logical slot count -------------------------------------------------------
	// src: https://www.postgresql.org/docs/current/view-pg-replication-slots.html
	var logicalSlotCount int
	if err := p.Conn.QueryRow(ctx,
		"SELECT count(*) FROM pg_replication_slots WHERE slot_type = 'logical'",
	).Scan(&logicalSlotCount); err != nil {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Could not count logical replication slots",
			Message: fmt.Sprintf("Could not query pg_replication_slots for logical slots: %v", err),
			DocRef:  docReplicationSlots,
		})
		logicalSlotCount = -1 // sentinel: query failed
	} else if logicalSlotCount > 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   fmt.Sprintf("Logical decoding is in use (%d logical slot(s))", logicalSlotCount),
			Message: fmt.Sprintf("pg_replication_slots shows %d logical replication slot(s). Logical decoding is active on this server.", logicalSlotCount),
			Detail:  "Ensure logical replication consumers (subscribers, CDC tools) are active. Inactive logical slots retain WAL indefinitely.",
			DocRef:  docReplicationSlots,
		})
	}

	// --- Publication count --------------------------------------------------------
	// src: https://www.postgresql.org/docs/current/catalog-pg-publication.html
	var pubCount int
	if err := p.Conn.QueryRow(ctx, "SELECT count(*) FROM pg_publication").Scan(&pubCount); err != nil {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Could not count publications",
			Message: fmt.Sprintf("Could not query pg_publication: %v", err),
			DocRef:  docLogicalReplication,
		})
		pubCount = -1 // sentinel: query failed
	} else if pubCount > 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   fmt.Sprintf("Logical replication publisher: %d publication(s) defined", pubCount),
			Message: fmt.Sprintf("pg_publication shows %d publication(s). This server is publishing data via logical replication.", pubCount),
			DocRef:  docLogicalReplication,
		})
	}

	// --- Subscription count -------------------------------------------------------
	// src: https://www.postgresql.org/docs/current/catalog-pg-subscription.html
	var subCount int
	if err := p.Conn.QueryRow(ctx, "SELECT count(*) FROM pg_subscription").Scan(&subCount); err != nil {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Could not count subscriptions",
			Message: fmt.Sprintf("Could not query pg_subscription: %v", err),
			DocRef:  docLogicalReplication,
		})
		subCount = -1 // sentinel: query failed
	} else if subCount > 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   fmt.Sprintf("Logical replication subscriber: %d subscription(s) active", subCount),
			Message: fmt.Sprintf("pg_subscription shows %d subscription(s). This server is subscribing to logical replication from one or more publishers.", subCount),
			DocRef:  docLogicalReplication,
		})
		// Query pg_stat_subscription for apply worker status (best-effort; count
		// active workers only — widely available since PG10).
		// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-SUBSCRIPTION-VIEW
		var activeWorkers int
		if err := p.Conn.QueryRow(ctx,
			"SELECT count(*) FROM pg_stat_subscription WHERE pid IS NOT NULL",
		).Scan(&activeWorkers); err != nil {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Unknown,
				Section: section,
				Title:   "Could not query pg_stat_subscription apply workers",
				Message: fmt.Sprintf("Could not query pg_stat_subscription: %v", err),
				DocRef:  docLogicalReplication,
			})
		}
		// No separate advice for active workers: if sub count > 0 the Info above
		// is sufficient on an idle dev instance. Unhealthy workers (pid IS NULL)
		// are a future Epic 4 concern.
	}

	// --- wal_level vs logical -----------------------------------------------------
	// Logical replication requires wal_level='logical'. If logical slots or
	// publications are present but wal_level is insufficient, warn loudly.
	// Only run this check when both counts are known (not -1 sentinel).
	// src: https://www.postgresql.org/docs/current/runtime-config-wal.html#GUC-WAL-LEVEL
	if logicalSlotCount >= 0 && pubCount >= 0 {
		walLevel, _ := p.Settings.Get("wal_level")
		if a := evalWalLevelVsLogical(walLevel, logicalSlotCount, pubCount); a != nil {
			out = append(out, *a)
		}
	}

	return out
}
