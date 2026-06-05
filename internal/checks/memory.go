package checks

import (
	"context"
	"fmt"

	"github.com/OpenRemedy/pgtuner/internal/osinspect"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// docMemResource is the canonical reference for memory GUCs.
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html
const docMemResource = "https://www.postgresql.org/docs/current/runtime-config-resource.html"

// docMaintenanceWorkMem17 references the TID-store improvements in PG17.
// src: https://www.postgresql.org/docs/17/runtime-config-resource.html
const docMaintenanceWorkMem17 = "https://www.postgresql.org/docs/17/runtime-config-resource.html"

// Shared-buffers sizing guidance from PostgreSQL docs:
//   "A reasonable starting value is 25% of RAM."
//   "Larger values may help, but each additional 1% above 40% usually gives
//    diminishing returns."
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-SHARED-BUFFERS

const (
	sharedBufsLowFrac  = 0.15 // below 15 % → Warn (too small)
	sharedBufsHighFrac = 0.40 // above 40 % → Warn (diminishing returns)
)

// effective_cache_size: recommended to reflect OS + PG cache combined.
// Values below 50 % of RAM cause the query planner to underestimate cache
// effectiveness and choose sub-optimal plans.
// src: https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-EFFECTIVE-CACHE-SIZE
const effectiveCacheLowFrac = 0.50

// maintenance_work_mem threshold: on a host with > 8 GiB RAM, keeping
// maintenance_work_mem below 64 MiB slows VACUUM and index builds
// disproportionately. The documented default (64 MB) is appropriate for small
// systems; larger hosts benefit from 256 MiB–1 GiB.
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-MAINTENANCE-WORK-MEM
const (
	maintWorkMemMinBytes = 64 * 1024 * 1024   // 64 MiB
	bigHostMinBytes      = 8 * 1024 * 1024 * 1024 // 8 GiB
)

// pg_settings unit note (confirmed empirically and per docs):
//   shared_buffers, effective_cache_size: unit = "8kB"  → setting × 8192 = bytes
//   work_mem, maintenance_work_mem:       unit = "kB"   → setting × 1024 = bytes
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html

// MemoryCheck evaluates shared_buffers, effective_cache_size, and
// maintenance_work_mem against total host RAM when available.
type MemoryCheck struct{ clusterScoped }

func (MemoryCheck) Name() string { return "memory" }

// Run checks memory-related GUCs and returns advice. It never panics and
// never returns an error; when OS facts are unavailable it emits Info with
// the raw configured values so the operator can judge.
func (MemoryCheck) Run(_ context.Context, p *Probe) []pgtuner.Advice {
	const section = "Memory"
	var out []pgtuner.Advice

	// shared_buffers: unit = 8kB blocks (setting × 8192 = bytes)
	sharedBufBlocks, hasSB := p.Settings.Int("shared_buffers")
	sharedBufBytes := sharedBufBlocks * 8192

	// effective_cache_size: unit = 8kB blocks (setting × 8192 = bytes)
	ecBlocks, hasEC := p.Settings.Int("effective_cache_size")
	ecBytes := ecBlocks * 8192

	// maintenance_work_mem: unit = kB (setting × 1024 = bytes)
	maintWorkMemKB, hasMWM := p.Settings.Int("maintenance_work_mem")
	maintWorkMemBytes := maintWorkMemKB * 1024

	// Use the cgroup-aware effective memory limit so container-constrained
	// deployments receive correctly-scaled advice.
	// src: https://docs.kernel.org/admin-guide/cgroup-v2.html (memory.max)
	// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-SHARED-BUFFERS
	effMem, constrained, err := p.OS.EffectiveMemoryBytes()

	if err == osinspect.ErrNoOSFacts {
		// Graceful degradation: report raw values without ratio advice.
		if hasSB {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Info,
				Section: section,
				Title:   "shared_buffers (no OS facts)",
				Message: fmt.Sprintf("shared_buffers is set to %s; cannot compute ratio without total RAM.", mibStr(sharedBufBytes)),
				DocRef:  docMemResource,
			})
		}
		if hasEC {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Info,
				Section: section,
				Title:   "effective_cache_size (no OS facts)",
				Message: fmt.Sprintf("effective_cache_size is set to %s; cannot verify planner estimate without total RAM.", mibStr(ecBytes)),
				DocRef:  docMemResource,
			})
		}
		if hasMWM {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Info,
				Section: section,
				Title:   "maintenance_work_mem (no OS facts)",
				Message: fmt.Sprintf("maintenance_work_mem is set to %s; cannot check against host RAM.", mibStr(maintWorkMemBytes)),
				DocRef:  docMemResource,
			})
		}
		return out
	}

	// OS facts available — run ratio checks.
	total := int64(effMem)
	if total <= 0 {
		// Guard against a bogus inspector returning zero.
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Cannot evaluate memory settings",
			Message: "OS inspector returned effective memory=0; skipping memory ratio checks.",
		}}
	}

	// memLabel returns a human-readable label for the memory budget source so
	// advice messages clearly distinguish cgroup limits from total host RAM.
	// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-SHARED-BUFFERS
	memLabel := func() string {
		if constrained {
			return fmt.Sprintf("cgroup memory limit (%s)", mibStr(total))
		}
		return fmt.Sprintf("total RAM (%s)", mibStr(total))
	}

	// --- shared_buffers -------------------------------------------------------
	// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-SHARED-BUFFERS
	if hasSB {
		frac := float64(sharedBufBytes) / float64(total)
		switch {
		case frac < sharedBufsLowFrac:
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Warn,
				Section: section,
				Title:   "shared_buffers is too low",
				Message: fmt.Sprintf("shared_buffers=%s is %.1f%% of %s; recommended starting point is 25%%. Too-small shared_buffers forces PostgreSQL to rely entirely on the OS page cache.", mibStr(sharedBufBytes), frac*100, memLabel()),
				Detail:  "Set shared_buffers to ~25% of available memory and restart PostgreSQL.",
				DocRef:  docMemResource,
			})
		case frac > sharedBufsHighFrac:
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Warn,
				Section: section,
				Title:   "shared_buffers may be too high",
				Message: fmt.Sprintf("shared_buffers=%s is %.1f%% of %s; above 40%% yields diminishing returns and leaves less memory for the OS page cache and per-backend work.", mibStr(sharedBufBytes), frac*100, memLabel()),
				Detail:  "Consider reducing shared_buffers to 25–40% of available memory.",
				DocRef:  docMemResource,
			})
		default:
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.OK,
				Section: section,
				Title:   "shared_buffers is well-sized",
				Message: fmt.Sprintf("shared_buffers=%s (%.1f%% of %s) is within the recommended 15–40%% range.", mibStr(sharedBufBytes), frac*100, memLabel()),
				DocRef:  docMemResource,
			})
		}
	}

	// --- effective_cache_size -------------------------------------------------
	// src: https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-EFFECTIVE-CACHE-SIZE
	if hasEC {
		frac := float64(ecBytes) / float64(total)
		if frac < effectiveCacheLowFrac {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Info,
				Section: section,
				Title:   "effective_cache_size may be underestimated",
				Message: fmt.Sprintf("effective_cache_size=%s is %.1f%% of %s; recommended value is 50–75%% to accurately reflect OS + PG cache. An underestimate causes the planner to favour sequential scans over index scans.", mibStr(ecBytes), frac*100, memLabel()),
				Detail:  "Set effective_cache_size to 50–75% of available memory.",
				DocRef:  docMemResource,
			})
		}
	}

	// --- maintenance_work_mem -------------------------------------------------
	// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-MAINTENANCE-WORK-MEM
	if hasMWM && total > bigHostMinBytes && maintWorkMemBytes < maintWorkMemMinBytes {
		docRef := docMemResource
		pg17note := ""
		if p.Version.AtLeast(17, 0) {
			// PG17 uses maintenance_work_mem more efficiently via TID-store for
			// vacuum. A larger value yields even greater benefit.
			// src: https://www.postgresql.org/docs/17/runtime-config-resource.html
			docRef = docMaintenanceWorkMem17
			pg17note = " On PostgreSQL 17+, VACUUM's TID-store uses maintenance_work_mem more efficiently, making this setting even more impactful."
		}
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "maintenance_work_mem is low for this host",
			Message: fmt.Sprintf("maintenance_work_mem=%s is below 64 MiB on a host with %s. VACUUM, CREATE INDEX, and REINDEX operations will be slower than necessary.%s", mibStr(maintWorkMemBytes), memLabel(), pg17note),
			Detail:  "Consider raising maintenance_work_mem to 256 MiB–1 GiB on hosts with several GiB of RAM.",
			DocRef:  docRef,
		})
	}

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.OK,
			Section: section,
			Title:   "Memory settings look healthy",
			Message: "shared_buffers, effective_cache_size, and maintenance_work_mem are within recommended ranges.",
		})
	}
	return out
}

// mibStr formats bytes as "X MiB" for human-readable advice messages.
func mibStr(b int64) string {
	mib := float64(b) / (1024 * 1024)
	if mib >= 1024 {
		return fmt.Sprintf("%.1f GiB", mib/1024)
	}
	return fmt.Sprintf("%.0f MiB", mib)
}
