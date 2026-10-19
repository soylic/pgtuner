package checks

import (
	"context"
	"fmt"

	"github.com/soylic/pgtuner/internal/pg"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// src: https://www.postgresql.org/docs/current/runtime-config-preset.html
const docDataChecksums = "https://www.postgresql.org/docs/current/runtime-config-preset.html"

// PG18 made checksums the initdb default.
// src: https://www.postgresql.org/docs/18/runtime-config-preset.html
const docDataChecksumsPG18 = "https://www.postgresql.org/docs/18/runtime-config-preset.html"

// src: https://www.postgresql.org/docs/14/runtime-config-client.html
const docTOASTCompression = "https://www.postgresql.org/docs/14/runtime-config-client.html"

// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-DATABASE-VIEW
const docTempFiles = "https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-DATABASE-VIEW"

// src: https://www.postgresql.org/docs/current/runtime-config-logging.html#GUC-LOG-TEMP-FILES
const docLogTempFiles = "https://www.postgresql.org/docs/current/runtime-config-logging.html#GUC-LOG-TEMP-FILES"

// tempFileSizeWarnThreshold is the cumulative temp-file size above which we warn.
// 1 GiB with a nonzero temp_files count suggests work_mem is too small.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-DATABASE-VIEW
const tempFileSizeWarnThreshold int64 = 1 * 1024 * 1024 * 1024 // 1 GiB

// StorageSettingsCheck evaluates cluster-wide storage GUCs. It is ClusterScope so
// -all-databases reports it once.
type StorageSettingsCheck struct{ clusterScoped }

func (StorageSettingsCheck) Name() string { return "storage_settings" }

// StorageCheck evaluates per-database temp-file spill statistics from
// pg_stat_database.
type StorageCheck struct{ databaseScoped }

func (StorageCheck) Name() string { return "storage" }

// evalTempFiles returns temp-file advice from pre-fetched pg_stat_database counters.
func evalTempFiles(tempFiles, tempBytes int64) []pgtuner.Advice {
	const section = "Storage"
	// Require both a nonzero count and significant bytes. One tiny temp file is not a problem.
	if tempFiles > 0 && tempBytes > tempFileSizeWarnThreshold {
		return []pgtuner.Advice{{
			ID:      "storage.temp-files-spilling",
			Level:   pgtuner.Warn,
			Section: section,
			Title:   "Queries are spilling to temp files",
			Message: fmt.Sprintf("pg_stat_database reports %d temp file(s) totalling %.1f GiB for the current database. Query sorts and hash operations that exceed work_mem spill to disk. This increases latency.", tempFiles, float64(tempBytes)/float64(1<<30)),
			Detail:  "Raise work_mem for sessions or queries that perform large sorts or joins. Alternatively, find and optimize the queries responsible. Check pg_stat_statements for queries with high temp_blks_written.",
			DocRef:  docTempFiles,
		}}
	}
	return nil
}

// evalStorageSettings returns settings-based storage advice (data_checksums,
// default_toast_compression, log_temp_files).
func evalStorageSettings(s *pg.Settings, v pg.ServerVersion) []pgtuner.Advice {
	const section = "Storage"
	var out []pgtuner.Advice

	// data_checksums is a preset GUC: initdb sets it and it cannot change at runtime.
	// src: https://www.postgresql.org/docs/current/runtime-config-preset.html
	// src: https://www.postgresql.org/docs/18/runtime-config-preset.html
	if dc, ok := s.Get("data_checksums"); ok && dc == "off" {
		if v.AtLeast(18, 0) {
			out = append(out, pgtuner.Advice{
				ID:      "storage_settings.data-checksums-off-pg18",
				Level:   pgtuner.Warn,
				Section: section,
				Title:   "Data checksums are disabled",
				Message: "data_checksums=off. Since PostgreSQL 18, initdb enables checksums by default. Without checksums, silent storage corruption (bit rot, controller bugs) can go undetected and spread to backups.",
				Detail:  "Run pg_checksums --enable on a stopped cluster (PG12+) or reinitialize the cluster. In PG18+, prefer initdb --checksums.",
				DocRef:  docDataChecksumsPG18,
			})
		} else {
			out = append(out, pgtuner.Advice{
				ID:      "storage_settings.data-checksums-off",
				Level:   pgtuner.Info,
				Section: section,
				Title:   "Data checksums are disabled",
				Message: "data_checksums=off. Without checksums, silent storage corruption (bit rot, faulty RAID controllers, bad cables) can go undetected and spread to backups.",
				Detail:  "Run pg_checksums --enable on a stopped cluster (PostgreSQL 12+). This is a one-time offline operation.",
				DocRef:  docDataChecksums,
			})
		}
	}

	// pglz remains the default for compatibility with older PG versions.
	// src: https://www.postgresql.org/docs/14/runtime-config-client.html
	if v.AtLeast(14, 0) {
		if dtc, ok := s.Get("default_toast_compression"); ok && dtc == "pglz" {
			out = append(out, pgtuner.Advice{
				ID:      "storage_settings.toast-compression-pglz",
				Level:   pgtuner.Info,
				Section: section,
				Title:   "Consider lz4 for TOAST compression",
				Message: "default_toast_compression=pglz (legacy default). For variable-length column data, lz4 generally compresses and decompresses faster at comparable compression ratios.",
				Detail:  "Set default_toast_compression=lz4 for faster TOAST compression. PostgreSQL does not recompress existing TOAST data. To rewrite old TOASTed values, run VACUUM FULL on the affected tables.",
				DocRef:  docTOASTCompression,
			})
		}
	}

	// src: https://www.postgresql.org/docs/current/runtime-config-logging.html#GUC-LOG-TEMP-FILES
	if ltf, ok := s.Int("log_temp_files"); ok && ltf == -1 {
		out = append(out, pgtuner.Advice{
			ID:      "storage_settings.log-temp-files-off",
			Level:   pgtuner.Info,
			Section: section,
			Title:   "log_temp_files is disabled",
			Message: "log_temp_files=-1 means PostgreSQL never logs temp-file spills. Enabling it helps to find queries that exceed work_mem without continuous monitoring.",
			Detail:  "Set log_temp_files=0 to log all temp file creation. Alternatively, set a threshold in kilobytes (for example 65536 for 64 MiB) to reduce log noise.",
			DocRef:  docLogTempFiles,
		})
	}

	return out
}

func (StorageSettingsCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "Storage"

	out := evalStorageSettings(p.Settings, p.Version)

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			ID:      "storage_settings.ok",
			Level:   pgtuner.OK,
			Section: section,
			Title:   "Storage settings look healthy",
			Message: "No storage-setting concerns detected. Data checksums, TOAST compression, and temp-file logging are all within acceptable ranges.",
			DocRef:  docDataChecksums,
		})
	}
	return out
}

// Run reads the current database's temp-file statistics. A query or scan failure
// yields one Unknown advice.
func (StorageCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "Storage"

	var out []pgtuner.Advice

	const tempSQL = "SELECT temp_files, temp_bytes FROM pg_stat_database WHERE datname = current_database()"
	var tempFiles, tempBytes int64
	if err := p.Conn.QueryRow(ctx, tempSQL).Scan(&tempFiles, &tempBytes); err != nil {
		out = append(out, pgtuner.Advice{
			ID:      "storage.temp-files-query-failed",
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Temp-file stats unavailable",
			Message: fmt.Sprintf("Could not query pg_stat_database for temp-file statistics: %v", err),
		})
	} else {
		out = append(out, evalTempFiles(tempFiles, tempBytes)...)
	}

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			ID:      "storage.ok",
			Level:   pgtuner.OK,
			Section: section,
			Title:   "Temp-file usage looks healthy",
			Message: "No significant temp-file spills detected for this database. Query sorts and hash operations stay within work_mem.",
			DocRef:  docTempFiles,
		})
	}
	return out
}
