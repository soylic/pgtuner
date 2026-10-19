package checks

import (
	"context"
	"fmt"

	"github.com/soylic/pgtuner/internal/pg"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

const docCheckpointStats = "https://www.postgresql.org/docs/current/monitoring-stats.html"

const docCheckpointTuning = "https://www.postgresql.org/docs/current/runtime-config-wal.html"

// checkpointReqRatioWarnThreshold: above 30 % requested checkpoints, WAL fills
// faster than the schedule drains it, which indicates a small max_wal_size.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html (pg_stat_bgwriter / pg_stat_checkpointer)
const checkpointReqRatioWarnThreshold = 0.30 // 30 %

// checkpointCompletionTargetDefault is the default since PG14. Lower values
// finish checkpoints sooner and raise write pressure.
// src: https://www.postgresql.org/docs/current/runtime-config-wal.html#GUC-CHECKPOINT-COMPLETION-TARGET
const checkpointCompletionTargetDefault = 0.9

// CheckpointsCheck evaluates checkpoint frequency and completion-target settings.
// PG17 moved and renamed the checkpoint columns from pg_stat_bgwriter to
// pg_stat_checkpointer.
// src: https://www.postgresql.org/docs/17/monitoring-stats.html
type CheckpointsCheck struct{ clusterScoped }

func (CheckpointsCheck) Name() string { return "checkpoints" }

// checkpointQuery returns the SQL for timed and requested checkpoint counts.
// PG17+: pg_stat_checkpointer.num_timed / num_requested.
// PG<17: pg_stat_bgwriter.checkpoints_timed / checkpoints_req.
// src: https://www.postgresql.org/docs/17/monitoring-stats.html
func checkpointQuery(v pg.ServerVersion) string {
	if v.AtLeast(17, 0) {
		return "SELECT num_timed, num_requested FROM pg_stat_checkpointer"
	}
	return "SELECT checkpoints_timed, checkpoints_req FROM pg_stat_bgwriter"
}

// evalCheckpoints turns checkpoint counters and checkpoint_completion_target into advice.
func evalCheckpoints(timed, requested int64, completionTarget float64) []pgtuner.Advice {
	const section = "Checkpoints"
	var out []pgtuner.Advice

	total := timed + requested

	if total == 0 {
		out = append(out, pgtuner.Advice{
			ID:      "checkpoints.no-activity",
			Level:   pgtuner.Info,
			Section: section,
			Title:   "No checkpoint activity recorded yet",
			Message: "The server recorded no checkpoints since statistics were last reset. This is normal on a freshly started or stats-reset server.",
			DocRef:  docCheckpointStats,
		})
	} else {
		ratio := float64(requested) / float64(total)
		if ratio > checkpointReqRatioWarnThreshold {
			out = append(out, pgtuner.Advice{
				ID:      "checkpoints.requested-too-frequent",
				Level:   pgtuner.Warn,
				Section: section,
				Title:   "Too many requested checkpoints",
				Message: fmt.Sprintf("%.0f%% of checkpoints are requested by backends (requested=%d, timed=%d, total=%d). Requested checkpoints occur when WAL fills max_wal_size before the scheduled interval. This causes write spikes.", ratio*100, requested, timed, total),
				Detail:  "Raise max_wal_size to let checkpoints run on schedule. Consider also raising checkpoint_timeout if write volume justifies it.",
				DocRef:  docCheckpointStats,
			})
		}
	}

	// Values below the 0.9 default concentrate checkpoint writes into a shorter window.
	// src: https://www.postgresql.org/docs/current/runtime-config-wal.html#GUC-CHECKPOINT-COMPLETION-TARGET
	if completionTarget < checkpointCompletionTargetDefault {
		out = append(out, pgtuner.Advice{
			ID:      "checkpoints.completion-target-low",
			Level:   pgtuner.Info,
			Section: section,
			Title:   "checkpoint_completion_target is below recommended value",
			Message: fmt.Sprintf("checkpoint_completion_target=%.2f is below the recommended 0.9 (the default since PostgreSQL 14). A lower value forces checkpoints to complete in a shorter fraction of the checkpoint interval. This concentrates disk writes.", completionTarget),
			Detail:  "Set checkpoint_completion_target=0.9 to spread checkpoint writes over 90% of the checkpoint interval.",
			DocRef:  docCheckpointTuning,
		})
	}

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			ID:      "checkpoints.settings-ok",
			Level:   pgtuner.OK,
			Section: section,
			Title:   "Checkpoint configuration looks healthy",
			Message: fmt.Sprintf("Requested-checkpoint ratio is within normal range (requested=%d, timed=%d) and checkpoint_completion_target=%.2f is adequate.", requested, timed, completionTarget),
			DocRef:  docCheckpointStats,
		})
	}
	return out
}

// Run returns a single Unknown advice on query or scan failure.
// It never panics and never returns an error.
func (CheckpointsCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "Checkpoints"

	sql := checkpointQuery(p.Version)

	var timed, requested int64
	if err := p.Conn.QueryRow(ctx, sql).Scan(&timed, &requested); err != nil {
		return []pgtuner.Advice{{
			ID:      "checkpoints.stats-query-failed",
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Checkpoint stats unavailable",
			Message: fmt.Sprintf("Could not query checkpoint statistics: %v", err),
		}}
	}

	completionTarget, _ := p.Settings.Float("checkpoint_completion_target")

	return evalCheckpoints(timed, requested, completionTarget)
}
