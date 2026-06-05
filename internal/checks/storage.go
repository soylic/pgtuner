package checks

import (
	"context"
	"fmt"

	"github.com/OpenRemedy/pgtuner/internal/pg"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// docDataChecksums is the canonical reference for data_checksums.
// src: https://www.postgresql.org/docs/current/runtime-config-preset.html
const docDataChecksums = "https://www.postgresql.org/docs/current/runtime-config-preset.html"

// docDataChecksumsPG18 references PG18 release notes where checksums became
// the initdb default.
// src: https://www.postgresql.org/docs/18/runtime-config-preset.html
const docDataChecksumsPG18 = "https://www.postgresql.org/docs/18/runtime-config-preset.html"

// docTOASTCompression is the canonical reference for default_toast_compression.
// src: https://www.postgresql.org/docs/14/runtime-config-client.html
const docTOASTCompression = "https://www.postgresql.org/docs/14/runtime-config-client.html"

// docTempFiles is the canonical reference for temp-file statistics.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-DATABASE-VIEW
const docTempFiles = "https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-DATABASE-VIEW"

// docLogTempFiles is the canonical reference for log_temp_files.
// src: https://www.postgresql.org/docs/current/runtime-config-logging.html#GUC-LOG-TEMP-FILES
const docLogTempFiles = "https://www.postgresql.org/docs/current/runtime-config-logging.html#GUC-LOG-TEMP-FILES"

// tempFileSizeWarnThreshold is the cumulative temp-file size above which we
// warn that queries are spilling significantly. 1 GiB of temp bytes alongside
// any nonzero temp_files count indicates work_mem is likely too small for the
// workload. Community practice treats any sustained temp-file growth as
// a sign that work_mem needs attention.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-DATABASE-VIEW
const tempFileSizeWarnThreshold int64 = 1 * 1024 * 1024 * 1024 // 1 GiB

// StorageSettingsCheck evaluates cluster-wide storage GUCs: data checksums,
// TOAST compression, and temp-file logging. These are instance-level settings,
// so under -all-databases they must be reported once, not repeated per database.
type StorageSettingsCheck struct{ clusterScoped }

func (StorageSettingsCheck) Name() string { return "storage_settings" }

// StorageCheck evaluates per-database temp-file spill statistics from
// pg_stat_database. These counters are database-scoped, so under -all-databases
// the check runs once per user database.
type StorageCheck struct{ databaseScoped }

func (StorageCheck) Name() string { return "storage" }

// evalTempFiles is the pure decision function for temp-file advice.
// It accepts pre-fetched pg_stat_database counters and returns advice
// without touching the database.
func evalTempFiles(tempFiles, tempBytes int64) []pgtuner.Advice {
	const section = "Storage"
	// Only warn when both conditions are met: nonzero spill count AND significant
	// total bytes. A single tiny temp file is not necessarily a problem.
	// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-DATABASE-VIEW
	if tempFiles > 0 && tempBytes > tempFileSizeWarnThreshold {
		return []pgtuner.Advice{{
			Level:   pgtuner.Warn,
			Section: section,
			Title:   "Queries are spilling to temp files",
			Message: fmt.Sprintf("pg_stat_database reports %d temp file(s) totalling %.1f GiB for the current database. Query sorts and hash operations that exceed work_mem spill to disk, increasing latency.", tempFiles, float64(tempBytes)/float64(1<<30)),
			Detail:  "Raise work_mem for sessions or queries that perform large sorts/joins, or identify and optimise the queries responsible. Check pg_stat_statements for queries with high temp_blks_written.",
			DocRef:  docTempFiles,
		}}
	}
	return nil
}

// evalStorageSettings is the pure decision function for settings-based storage
// advice (data_checksums, default_toast_compression, log_temp_files).
// It does not touch the database and can be exhaustively unit-tested.
func evalStorageSettings(s *pg.Settings, v pg.ServerVersion) []pgtuner.Advice {
	const section = "Storage"
	var out []pgtuner.Advice

	// --- data_checksums --------------------------------------------------------
	// data_checksums is a preset GUC (set at initdb time, cannot be changed at
	// runtime). Off means silent data corruption in storage can go undetected.
	// In PG18 checksums became the initdb default, so absence is unusual.
	// src: https://www.postgresql.org/docs/current/runtime-config-preset.html
	// src: https://www.postgresql.org/docs/18/runtime-config-preset.html
	if dc, ok := s.Get("data_checksums"); ok && dc == "off" {
		if v.AtLeast(18, 0) {
			// PG18+: checksums are the initdb default; their absence is surprising.
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Warn,
				Section: section,
				Title:   "Data checksums are disabled",
				Message: "data_checksums=off. Since PostgreSQL 18, checksum-enabled clusters are the initdb default. Without checksums, silent storage corruption (bit rot, controller bugs) can go undetected and spread to backups.",
				Detail:  "Enable checksums by running pg_checksums --enable on a stopped cluster (PG12+) or reinitialise. In PG18+ prefer initdb --checksums.",
				DocRef:  docDataChecksumsPG18,
			})
		} else {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Info,
				Section: section,
				Title:   "Data checksums are disabled",
				Message: "data_checksums=off. Without checksums, silent storage corruption (bit rot, faulty RAID controllers, bad cables) can go undetected and propagate to backups.",
				Detail:  "Enable checksums by running pg_checksums --enable on a stopped cluster (PostgreSQL 12+). This is a one-time offline operation.",
				DocRef:  docDataChecksums,
			})
		}
	}

	// --- default_toast_compression (PG≥14) ------------------------------------
	// lz4 TOAST compression is faster than pglz at comparable ratios for most
	// data types. pglz remains the default for compatibility with older PG versions.
	// src: https://www.postgresql.org/docs/14/runtime-config-client.html
	if v.AtLeast(14, 0) {
		if dtc, ok := s.Get("default_toast_compression"); ok && dtc == "pglz" {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Info,
				Section: section,
				Title:   "Consider lz4 for TOAST compression",
				Message: "default_toast_compression=pglz (legacy default). lz4 generally offers faster compression and decompression at comparable compression ratios for variable-length column data.",
				Detail:  "Set default_toast_compression=lz4 to benefit from faster TOAST compression. Existing TOAST data is not recompressed; run VACUUM FULL on affected tables if you want to rewrite old TOASTed values.",
				DocRef:  docTOASTCompression,
			})
		}
	}

	// --- log_temp_files --------------------------------------------------------
	// log_temp_files=-1 (disabled) means temp-file spills are never logged.
	// Enabling it (e.g. log_temp_files=0 to log all, or a size threshold in kB)
	// makes spill events visible in the server log for diagnosis.
	// src: https://www.postgresql.org/docs/current/runtime-config-logging.html#GUC-LOG-TEMP-FILES
	if ltf, ok := s.Int("log_temp_files"); ok && ltf == -1 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "log_temp_files is disabled",
			Message: "log_temp_files=-1 means temp-file spills are never logged. Enabling it helps identify queries that exceed work_mem without requiring continuous monitoring.",
			Detail:  "Set log_temp_files=0 to log all temp file creation, or to a kilobyte threshold (e.g. 65536 for 64 MiB) to reduce log noise.",
			DocRef:  docLogTempFiles,
		})
	}

	return out
}

// Run evaluates the cluster-wide storage settings (data_checksums,
// default_toast_compression, log_temp_files). When none fire it emits an OK.
// It never panics and never returns an error.
func (StorageSettingsCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "Storage"

	out := evalStorageSettings(p.Settings, p.Version)

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.OK,
			Section: section,
			Title:   "Storage settings look healthy",
			Message: "No storage-setting concerns detected: data checksums, TOAST compression, and temp-file logging are all within acceptable ranges.",
			DocRef:  docDataChecksums,
		})
	}
	return out
}

// Run queries the current database's temp-file spill statistics from
// pg_stat_database. On query or scan failure it returns a single Unknown-level
// advice. When no spill is detected it emits an OK.
// It never panics and never returns an error.
func (StorageCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	const section = "Storage"

	var out []pgtuner.Advice

	// --- temp files (stats query) ---------------------------------------------
	// Query pg_stat_database for the current database's cumulative temp-file stats.
	// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-DATABASE-VIEW
	const tempSQL = "SELECT temp_files, temp_bytes FROM pg_stat_database WHERE datname = current_database()"
	var tempFiles, tempBytes int64
	if err := p.Conn.QueryRow(ctx, tempSQL).Scan(&tempFiles, &tempBytes); err != nil {
		out = append(out, pgtuner.Advice{
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
			Level:   pgtuner.OK,
			Section: section,
			Title:   "Temp-file usage looks healthy",
			Message: "No significant temp-file spills detected for this database; query sorts and hash operations are staying within work_mem.",
			DocRef:  docTempFiles,
		})
	}
	return out
}
