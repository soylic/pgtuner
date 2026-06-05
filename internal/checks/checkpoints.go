package checks

import (
	"context"
	"fmt"

	"github.com/OpenRemedy/pgtuner/internal/pg"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// docCheckpointStats is the canonical reference for checkpoint monitoring columns.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html
const docCheckpointStats = "https://www.postgresql.org/docs/current/monitoring-stats.html"

// docCheckpointTuning is the canonical reference for checkpoint GUCs.
// src: https://www.postgresql.org/docs/current/runtime-config-wal.html
const docCheckpointTuning = "https://www.postgresql.org/docs/current/runtime-config-wal.html"

// checkpointReqRatioWarnThreshold is the fraction of requested (non-timed)
// checkpoints above which we warn. If more than 30% of checkpoints are
// requested by backends (rather than timed), WAL segments are being consumed
// faster than the checkpoint schedule can drain them, indicating max_wal_size
// is too small.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html (pg_stat_bgwriter / pg_stat_checkpointer)
const checkpointReqRatioWarnThreshold = 0.30 // 30 %

// checkpointCompletionTargetDefault is the PostgreSQL default since PG14.
// Values below 0.9 cause checkpoints to complete too quickly, generating
// more write pressure at checkpoint time.
// src: https://www.postgresql.org/docs/current/runtime-config-wal.html#GUC-CHECKPOINT-COMPLETION-TARGET
const checkpointCompletionTargetDefault = 0.9

// CheckpointsCheck evaluates checkpoint frequency and completion-target settings.
// It issues a version-branched query: PG≥17 uses pg_stat_checkpointer (the
// checkpoint columns were moved out of pg_stat_bgwriter in PG17 and renamed);
// PG<17 uses pg_stat_bgwriter.
// src: https://www.postgresql.org/docs/17/monitoring-stats.html
type CheckpointsCheck struct{ clusterScoped }

func (CheckpointsCheck) Name() string { return "checkpoints" }

// checkpointQuery returns the version-appropriate SQL to read timed and
// requested checkpoint counts. This is an unexported helper so it can be
// directly unit-tested for both PG16→bgwriter and PG17→checkpointer paths.
//
// PG≥17: pg_stat_checkpointer.num_timed / num_requested
// PG<17: pg_stat_bgwriter.checkpoints_timed / checkpoints_req
//
// src: https://www.postgresql.org/docs/17/monitoring-stats.html
func checkpointQuery(v pg.ServerVersion) string {
	if v.AtLeast(17, 0) {
		return "SELECT num_timed, num_requested FROM pg_stat_checkpointer"
	}
	return "SELECT checkpoints_timed, checkpoints_req FROM pg_stat_bgwriter"
}

// evalCheckpoints is the pure decision function. It accepts already-fetched
// checkpoint counters and the checkpoint_completion_target value, and returns
// advice without touching the database.
func evalCheckpoints(timed, requested int64, completionTarget float64) []pgtuner.Advice {
	const section = "Checkpoints"
	var out []pgtuner.Advice

	total := timed + requested

	// --- checkpoint activity --------------------------------------------------
	// src: https://www.postgresql.org/docs/current/monitoring-stats.html
	if total == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "No checkpoint activity recorded yet",
			Message: "No checkpoints have occurred since statistics were last reset. This is normal on a freshly started or stats-reset server.",
			DocRef:  docCheckpointStats,
		})
	} else {
		// ratio of requested (non-scheduled) checkpoints to total.
		ratio := float64(requested) / float64(total)
		if ratio > checkpointReqRatioWarnThreshold {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Warn,
				Section: section,
				Title:   "Too many requested checkpoints",
				Message: fmt.Sprintf("%.0f%% of checkpoints are requested by backends (requested=%d, timed=%d, total=%d). Requested checkpoints occur when WAL fills max_wal_size before the scheduled interval, causing write spikes.", ratio*100, requested, timed, total),
				Detail:  "Raise max_wal_size to allow checkpoints to happen on schedule. Consider also raising checkpoint_timeout if write volume justifies it.",
				DocRef:  docCheckpointStats,
			})
		}
	}

	// --- checkpoint_completion_target -----------------------------------------
	// Default is 0.9 since PG14; values below 0.9 concentrate checkpoint writes
	// into a shorter window, increasing I/O pressure.
	// src: https://www.postgresql.org/docs/current/runtime-config-wal.html#GUC-CHECKPOINT-COMPLETION-TARGET
	if completionTarget < checkpointCompletionTargetDefault {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "checkpoint_completion_target is below recommended value",
			Message: fmt.Sprintf("checkpoint_completion_target=%.2f is below the recommended 0.9 (the default since PostgreSQL 14). A lower value forces checkpoints to complete in a shorter fraction of the checkpoint interval, concentrating disk writes.", completionTarget),
			Detail:  "Set checkpoint_completion_target=0.9 to spread checkpoint writes over 90% of the checkpoint interval.",
			DocRef:  docCheckpointTuning,
		})
	}

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.OK,
			Section: section,
			Title:   "Checkpoint configuration looks healthy",
			Message: fmt.Sprintf("Requested-checkpoint ratio is within normal range (requested=%d, timed=%d) and checkpoint_completion_target=%.2f is adequate.", requested, timed, completionTarget),
			DocRef:  docCheckpointStats,
		})
	}
	return out
}

// Run performs the version-branched query and returns checkpoint advice.
// On query or scan failure it returns a single Unknown-level advice.
// It never panics and never returns an error.
func (CheckpointsCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "Checkpoints"

	sql := checkpointQuery(p.Version)

	var timed, requested int64
	if err := p.Conn.QueryRow(ctx, sql).Scan(&timed, &requested); err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Checkpoint stats unavailable",
			Message: fmt.Sprintf("Could not query checkpoint statistics: %v", err),
		}}
	}

	completionTarget, _ := p.Settings.Float("checkpoint_completion_target")

	return evalCheckpoints(timed, requested, completionTarget)
}
