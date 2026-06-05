package checks

import (
	"context"
	"strings"

	"github.com/OpenRemedy/pgtuner/internal/pg"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// docTrackIOTiming is the canonical reference for track_io_timing.
// src: https://www.postgresql.org/docs/current/runtime-config-statistics.html#GUC-TRACK-IO-TIMING
const docTrackIOTiming = "https://www.postgresql.org/docs/current/runtime-config-statistics.html#GUC-TRACK-IO-TIMING"

// docTrackCounts is the canonical reference for track_counts.
// src: https://www.postgresql.org/docs/current/runtime-config-statistics.html#GUC-TRACK-COUNTS
const docTrackCounts = "https://www.postgresql.org/docs/current/runtime-config-statistics.html#GUC-TRACK-COUNTS"

// docPGStatStatements is the canonical reference for pg_stat_statements.
// src: https://www.postgresql.org/docs/current/pgstatstatements.html
const docPGStatStatements = "https://www.postgresql.org/docs/current/pgstatstatements.html"

// StatisticsCheck evaluates statistics-collection configuration settings.
// It is a pure settings check: it does not query any statistics views.
type StatisticsCheck struct{ clusterScoped }

func (StatisticsCheck) Name() string { return "statistics" }

// evalStatistics is the pure decision function for statistics-configuration
// advice. It accepts a Settings snapshot and returns advice without touching
// the database. Each rule can be exhaustively unit-tested via pg.NewSettings.
func evalStatistics(s *pg.Settings) []pgtuner.Advice {
	const section = "Statistics"
	var out []pgtuner.Advice

	// --- track_io_timing -------------------------------------------------------
	// track_io_timing enables collection of cumulative I/O timing data in
	// pg_stat_database, pg_stat_bgwriter, and (PG≥16) pg_stat_io.
	// The overhead is low on modern kernels that use a cheap clock source such as
	// VDSO clock_gettime; it is off by default only for historical compatibility.
	// Enabling it is recommended for any server where I/O performance matters.
	// src: https://www.postgresql.org/docs/current/runtime-config-statistics.html#GUC-TRACK-IO-TIMING
	if tit, ok := s.Get("track_io_timing"); ok && tit == "off" {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "track_io_timing is disabled",
			Message: "track_io_timing=off. Enabling it populates read/write timing columns in pg_stat_database, pg_stat_bgwriter, and pg_stat_io, providing visibility into I/O latency at very low overhead on kernels with a fast clock source (e.g. VDSO clock_gettime).",
			Detail:  "Set track_io_timing=on. On modern Linux kernels the per-call overhead is sub-microsecond; measure on your hardware if overhead is a concern.",
			DocRef:  docTrackIOTiming,
		})
	}

	// --- track_counts ----------------------------------------------------------
	// track_counts controls collection of activity statistics used by autovacuum
	// to determine when to vacuum and analyze tables. Disabling it prevents
	// autovacuum from functioning correctly, leading to table bloat and eventually
	// transaction-ID wraparound danger.
	// src: https://www.postgresql.org/docs/current/runtime-config-statistics.html#GUC-TRACK-COUNTS
	if tc, ok := s.Get("track_counts"); ok && tc == "off" {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Bad,
			Section: section,
			Title:   "track_counts is disabled",
			Message: "track_counts=off. Autovacuum relies on row-level activity counters (rows inserted, updated, deleted) to decide when to vacuum and analyze tables. With track_counts disabled, autovacuum cannot function, leading to table bloat and eventual transaction-ID wraparound risk.",
			Detail:  "Set track_counts=on (the default). This setting should almost never be disabled in production.",
			DocRef:  docTrackCounts,
		})
	}

	// --- pg_stat_statements in shared_preload_libraries ------------------------
	// pg_stat_statements is the standard extension for tracking per-query
	// execution statistics. It must be listed in shared_preload_libraries so its
	// shared memory segment is allocated at server start. Its absence makes it
	// impossible to identify slow or frequently executed queries.
	// src: https://www.postgresql.org/docs/current/pgstatstatements.html
	if spl, ok := s.Get("shared_preload_libraries"); ok {
		if !strings.Contains(spl, "pg_stat_statements") {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Info,
				Section: section,
				Title:   "pg_stat_statements is not loaded",
				Message: "pg_stat_statements is not found in shared_preload_libraries. This extension tracks per-query execution statistics (call counts, total time, rows) and is the standard tool for identifying slow or high-frequency queries.",
				Detail:  "Add pg_stat_statements to shared_preload_libraries and restart PostgreSQL, then run CREATE EXTENSION IF NOT EXISTS pg_stat_statements in the target database.",
				DocRef:  docPGStatStatements,
			})
		}
	}

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.OK,
			Section: section,
			Title:   "Statistics configuration looks healthy",
			Message: "No statistics-configuration concerns detected.",
			DocRef:  docTrackCounts,
		})
	}
	return out
}

// Run checks statistics-collection settings and returns advice.
// This check is settings-only; it never queries statistics views and so
// does not need a database connection beyond what is already in the Probe.
// It never panics and never returns an error.
func (StatisticsCheck) Run(_ context.Context, p *Probe) []pgtuner.Advice {
	return evalStatistics(p.Settings)
}
