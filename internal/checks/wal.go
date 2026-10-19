package checks

import (
	"context"

	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// src: https://www.postgresql.org/docs/current/runtime-config-wal.html
const docWALRuntime = "https://www.postgresql.org/docs/current/runtime-config-wal.html"

// PG15 added the lz4 and zstd wal_compression options.
// src: https://www.postgresql.org/docs/15/runtime-config-wal.html#GUC-WAL-COMPRESSION
const docWALCompression15 = "https://www.postgresql.org/docs/15/runtime-config-wal.html#GUC-WAL-COMPRESSION"

// WALCheck evaluates WAL safety and efficiency settings.
type WALCheck struct{ clusterScoped }

func (WALCheck) Name() string { return "wal" }

// Run checks WAL-related GUCs. It reads settings only and runs no query.
func (WALCheck) Run(_ context.Context, p *Probe) []pgtuner.Advice {
	const section = "WAL"
	var out []pgtuner.Advice

	// src: https://www.postgresql.org/docs/current/runtime-config-wal.html#GUC-FSYNC
	if !p.Settings.Bool("fsync") {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Bad,
			Section: section,
			Title:   "fsync is disabled. A crash can corrupt the cluster.",
			Message: "fsync=off means PostgreSQL never flushes dirty pages to disk after writes. A crash or power loss can leave the data directory corrupted and unrecoverable.",
			Detail:  "Set fsync=on immediately. Disable it only in fully disposable test environments.",
			DocRef:  docWALRuntime,
		})
	}

	// src: https://www.postgresql.org/docs/current/runtime-config-wal.html#GUC-FULL-PAGE-WRITES
	if !p.Settings.Bool("full_page_writes") {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Bad,
			Section: section,
			Title:   "full_page_writes disabled",
			Message: "full_page_writes=off disables protection against partial-page writes after an OS or hardware crash. With fsync=on, this reduces WAL volume but risks silent data corruption on a crash in the middle of a write.",
			Detail:  "Set full_page_writes=on. Disable it only if the filesystem guarantees atomic page writes (for example ZFS with sync=always, or a CoW filesystem) and you tested the recovery path.",
			DocRef:  docWALRuntime,
		})
	}

	// Recent transactions (up to wal_writer_delay x2) can be lost on a crash, but the cluster stays consistent.
	// src: https://www.postgresql.org/docs/current/runtime-config-wal.html#GUC-SYNCHRONOUS-COMMIT
	if sc, ok := p.Settings.Get("synchronous_commit"); ok && sc == "off" {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "synchronous_commit=off trades durability for throughput",
			Message: "synchronous_commit=off: PostgreSQL acknowledges transactions before it flushes their WAL record. In a crash, committed transactions within the last wal_writer_delay window may be silently lost, but the cluster stays consistent. Is this intentional?",
			Detail:  "This is a safe trade-off for workloads that can lose a few recent commits (for example session data, counters). Set synchronous_commit=on for financial or audit data.",
			DocRef:  docWALRuntime,
		})
	}

	// Algorithms by version:
	//   PG15+: on, off, lz4, zstd, pglz
	//   PG14:  on, off, lz4
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
			detail = "Consider setting wal_compression=lz4 (available since PostgreSQL 14) or wal_compression=on (pglz) to reduce WAL volume."
			docRef = docWALRuntime
		default:
			detail = "Consider setting wal_compression=on to reduce WAL volume. This PostgreSQL version supports only pglz."
			docRef = docWALRuntime
		}
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "WAL compression is disabled",
			Message: "wal_compression=off. Enabling it reduces WAL volume (less disk I/O, smaller replication lag). The cost is a small amount of CPU at WAL-write time.",
			Detail:  detail,
			DocRef:  docRef,
		})
	}

	// minimal fits only single-server installations that need no backup or standby.
	// src: https://www.postgresql.org/docs/current/runtime-config-wal.html#GUC-WAL-LEVEL
	if wl, ok := p.Settings.Get("wal_level"); ok && wl == "minimal" {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "wal_level=minimal prevents replication and PITR",
			Message: "wal_level=minimal writes only the WAL that crash recovery needs. Streaming replication, standby servers, PITR (point-in-time recovery), and logical decoding are all unavailable.",
			Detail:  "Set wal_level=replica for streaming replication or PITR. Set wal_level=logical for logical replication.",
			DocRef:  docWALRuntime,
		})
	}

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.OK,
			Section: section,
			Title:   "WAL settings look healthy",
			Message: "fsync, full_page_writes, synchronous_commit, wal_compression, and wal_level are all within the recommended values.",
			DocRef:  docWALRuntime,
		})
	}
	return out
}
