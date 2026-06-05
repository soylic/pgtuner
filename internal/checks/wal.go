package checks

import (
	"context"

	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// docWALRuntime is the canonical reference for WAL-related GUCs.
// src: https://www.postgresql.org/docs/current/runtime-config-wal.html
const docWALRuntime = "https://www.postgresql.org/docs/current/runtime-config-wal.html"

// docWALCompression15 references PG15 wal_compression options (lz4/zstd added).
// src: https://www.postgresql.org/docs/15/runtime-config-wal.html#GUC-WAL-COMPRESSION
const docWALCompression15 = "https://www.postgresql.org/docs/15/runtime-config-wal.html#GUC-WAL-COMPRESSION"

// WALCheck evaluates WAL safety and efficiency settings.
type WALCheck struct{ clusterScoped }

func (WALCheck) Name() string { return "wal" }

// Run checks WAL-related GUCs and returns advice. It never panics and never
// returns an error; it is settings-only (no DB query).
func (WALCheck) Run(_ context.Context, p *Probe) []pgtuner.Advice {
	const section = "WAL"
	var out []pgtuner.Advice

	// --- fsync ------------------------------------------------------------------
	// fsync=off means PostgreSQL does not call fsync() after writes; a crash can
	// leave the data directory in a corrupt, unrecoverable state.
	// src: https://www.postgresql.org/docs/current/runtime-config-wal.html#GUC-FSYNC
	if !p.Settings.Bool("fsync") {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Bad,
			Section: section,
			Title:   "fsync disabled — crash can corrupt the cluster",
			Message: "fsync=off means PostgreSQL never flushes dirty pages to disk after writes. A crash or power loss can leave the data directory in a corrupted, unrecoverable state.",
			Detail:  "Enable fsync=on immediately. Only disable it in fully disposable test environments.",
			DocRef:  docWALRuntime,
		})
	}

	// --- full_page_writes -------------------------------------------------------
	// full_page_writes protects the cluster against partial-page writes after a
	// crash; disabling it risks silent data corruption in combination with fsync.
	// src: https://www.postgresql.org/docs/current/runtime-config-wal.html#GUC-FULL-PAGE-WRITES
	if !p.Settings.Bool("full_page_writes") {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Bad,
			Section: section,
			Title:   "full_page_writes disabled",
			Message: "full_page_writes=off disables protection against partial-page writes after an OS or hardware crash. Combined with fsync=on this reduces WAL volume but risks silent data corruption on a crash mid-write.",
			Detail:  "Enable full_page_writes=on unless you are using a filesystem that guarantees atomic page writes (e.g. ZFS with sync=always, or a CoW filesystem) and have tested your recovery path.",
			DocRef:  docWALRuntime,
		})
	}

	// --- synchronous_commit ----------------------------------------------------
	// synchronous_commit=off allows PostgreSQL to report success before the WAL
	// record is flushed to disk. Recent transactions (up to wal_writer_delay ×2)
	// can be lost on a crash; however the cluster remains consistent.
	// src: https://www.postgresql.org/docs/current/runtime-config-wal.html#GUC-SYNCHRONOUS-COMMIT
	if sc, ok := p.Settings.Get("synchronous_commit"); ok && sc == "off" {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "synchronous_commit=off — durability traded for throughput",
			Message: "synchronous_commit=off: transactions are acknowledged before their WAL record is flushed. In a crash, committed transactions within the last wal_writer_delay window may be silently lost (though the cluster stays consistent). Intentional?",
			Detail:  "This is a safe trade-off for workloads where losing a few recent commits is acceptable (e.g. session data, counters). Set synchronous_commit=on for financial or audit data.",
			DocRef:  docWALRuntime,
		})
	}

	// --- wal_compression -------------------------------------------------------
	// wal_compression reduces WAL volume with negligible CPU cost on modern hardware.
	// Available algorithms vary by version:
	//   PG≥15: on, off, lz4, zstd, pglz  (lz4/zstd added in PG15)
	//   PG14:  on, off, lz4               (lz4 added in PG14)
	//   PG<14: on, off
	// src: https://www.postgresql.org/docs/15/runtime-config-wal.html#GUC-WAL-COMPRESSION
	if wc, ok := p.Settings.Get("wal_compression"); ok && wc == "off" {
		var detail string
		var docRef string
		switch {
		case p.Version.AtLeast(15, 0):
			detail = "Consider setting wal_compression=lz4 or wal_compression=zstd for better throughput. Both lz4 and zstd are available on PostgreSQL 15+."
			docRef = docWALCompression15
		case p.Version.AtLeast(14, 0):
			detail = "Consider setting wal_compression=lz4 (available since PostgreSQL 14) or wal_compression=on (pglz) for reduced WAL volume."
			docRef = docWALRuntime
		default:
			detail = "Consider setting wal_compression=on to reduce WAL volume. Only pglz is available on this PostgreSQL version."
			docRef = docWALRuntime
		}
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "WAL compression is disabled",
			Message: "wal_compression=off; enabling it reduces WAL volume (less disk I/O, smaller replication lag) at the cost of a small amount of CPU at WAL-write time.",
			Detail:  detail,
			DocRef:  docRef,
		})
	}

	// --- wal_level -------------------------------------------------------------
	// wal_level=minimal disables replication, PITR, and logical decoding; it is
	// only appropriate for single-server installations with no backup/standby need.
	// src: https://www.postgresql.org/docs/current/runtime-config-wal.html#GUC-WAL-LEVEL
	if wl, ok := p.Settings.Get("wal_level"); ok && wl == "minimal" {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "wal_level=minimal: no replication or PITR possible",
			Message: "wal_level=minimal writes the absolute minimum WAL needed for crash recovery. Streaming replication, standby servers, PITR (point-in-time recovery), and logical decoding are all unavailable.",
			Detail:  "Set wal_level=replica for streaming replication or PITR, or wal_level=logical for logical replication.",
			DocRef:  docWALRuntime,
		})
	}

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.OK,
			Section: section,
			Title:   "WAL settings look healthy",
			Message: "fsync, full_page_writes, synchronous_commit, wal_compression, and wal_level are all within recommended parameters.",
			DocRef:  docWALRuntime,
		})
	}
	return out
}
