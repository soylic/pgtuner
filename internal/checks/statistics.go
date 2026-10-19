package checks

import (
	"context"
	"strings"

	"github.com/soylic/pgtuner/internal/pg"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// src: https://www.postgresql.org/docs/current/runtime-config-statistics.html#GUC-TRACK-IO-TIMING
const docTrackIOTiming = "https://www.postgresql.org/docs/current/runtime-config-statistics.html#GUC-TRACK-IO-TIMING"

// src: https://www.postgresql.org/docs/current/runtime-config-statistics.html#GUC-TRACK-COUNTS
const docTrackCounts = "https://www.postgresql.org/docs/current/runtime-config-statistics.html#GUC-TRACK-COUNTS"

// src: https://www.postgresql.org/docs/current/pgstatstatements.html
const docPGStatStatements = "https://www.postgresql.org/docs/current/pgstatstatements.html"

// StatisticsCheck evaluates statistics-collection settings. It queries no views.
type StatisticsCheck struct{ clusterScoped }

func (StatisticsCheck) Name() string { return "statistics" }

// evalStatistics returns statistics-configuration advice from a Settings snapshot.
func evalStatistics(s *pg.Settings) []pgtuner.Advice {
	const section = "Statistics"
	var out []pgtuner.Advice

	// Overhead is low with a cheap clock source such as VDSO clock_gettime. The
	// default is off only for historical compatibility. pg_stat_io exists in PG16+.
	// src: https://www.postgresql.org/docs/current/runtime-config-statistics.html#GUC-TRACK-IO-TIMING
	if tit, ok := s.Get("track_io_timing"); ok && tit == "off" {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "track_io_timing is disabled",
			Message: "track_io_timing=off. Enabling it fills the read and write timing columns in pg_stat_database, pg_stat_bgwriter, and pg_stat_io. These columns show I/O latency. The overhead is very low on kernels with a fast clock source (for example VDSO clock_gettime).",
			Detail:  "Set track_io_timing=on. On modern Linux kernels the overhead per call is sub-microsecond. If overhead is a concern, measure it on your hardware.",
			DocRef:  docTrackIOTiming,
		})
	}

	// Autovacuum uses these counters to decide when to vacuum and analyze.
	// src: https://www.postgresql.org/docs/current/runtime-config-statistics.html#GUC-TRACK-COUNTS
	if tc, ok := s.Get("track_counts"); ok && tc == "off" {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Bad,
			Section: section,
			Title:   "track_counts is disabled",
			Message: "track_counts=off. Autovacuum uses row-level activity counters (rows inserted, updated, deleted) to decide when to vacuum and analyze tables. With track_counts disabled, autovacuum cannot function. This leads to table bloat and, eventually, a risk of transaction-ID wraparound.",
			Detail:  "Set track_counts=on (the default). Almost never disable this setting in production.",
			DocRef:  docTrackCounts,
		})
	}

	// The extension must be in shared_preload_libraries so its shared memory is
	// allocated at server start.
	// src: https://www.postgresql.org/docs/current/pgstatstatements.html
	if spl, ok := s.Get("shared_preload_libraries"); ok {
		if !strings.Contains(spl, "pg_stat_statements") {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Info,
				Section: section,
				Title:   "pg_stat_statements is not loaded",
				Message: "pg_stat_statements is not in shared_preload_libraries. This extension tracks per-query execution statistics (call counts, total time, rows). It is the standard tool to find slow or high-frequency queries.",
				Detail:  "Add pg_stat_statements to shared_preload_libraries. Restart PostgreSQL. Then run CREATE EXTENSION IF NOT EXISTS pg_stat_statements in the target database.",
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

func (StatisticsCheck) Run(_ context.Context, p *Probe) []pgtuner.Advice {
	return evalStatistics(p.Settings)
}
