package checks

import (
	"context"
	"fmt"

	"github.com/soylic/pgtuner/internal/osinspect"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

const docMemResource = "https://www.postgresql.org/docs/current/runtime-config-resource.html"

// PG17 TID-store improvements.
const docMaintenanceWorkMem17 = "https://www.postgresql.org/docs/17/runtime-config-resource.html"

// The PostgreSQL docs suggest 25% of RAM as a starting value and note diminishing returns above 40%.
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-SHARED-BUFFERS

const (
	sharedBufsLowFrac  = 0.15
	sharedBufsHighFrac = 0.40
)

// effective_cache_size should reflect OS + PG cache combined. Below 50 % of RAM
// the planner underestimates caching and may choose poor plans.
// src: https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-EFFECTIVE-CACHE-SIZE
const effectiveCacheLowFrac = 0.50

// Above 8 GiB RAM, maintenance_work_mem below 64 MiB slows VACUUM and index builds.
// The 64 MB default suits small systems. Larger hosts benefit from 256 MiB–1 GiB.
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-MAINTENANCE-WORK-MEM
const (
	maintWorkMemMinBytes = 64 * 1024 * 1024       // 64 MiB
	bigHostMinBytes      = 8 * 1024 * 1024 * 1024 // 8 GiB
)

// pg_settings units:
//   shared_buffers, effective_cache_size: "8kB" → setting × 8192 = bytes
//   work_mem, maintenance_work_mem:       "kB"  → setting × 1024 = bytes
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html

// MemoryCheck evaluates shared_buffers, effective_cache_size, and
// maintenance_work_mem against host RAM.
type MemoryCheck struct{ clusterScoped }

func (MemoryCheck) Name() string { return "memory" }

// Run emits Info with the raw values when OS facts are unavailable.
// It never panics and never returns an error.
func (MemoryCheck) Run(_ context.Context, p *Probe) []pgtuner.Advice {
	const section = "Memory"
	var out []pgtuner.Advice

	sharedBufBlocks, hasSB := p.Settings.Int("shared_buffers")
	sharedBufBytes := sharedBufBlocks * 8192

	ecBlocks, hasEC := p.Settings.Int("effective_cache_size")
	ecBytes := ecBlocks * 8192

	maintWorkMemKB, hasMWM := p.Settings.Int("maintenance_work_mem")
	maintWorkMemBytes := maintWorkMemKB * 1024

	// The cgroup-aware limit scales the advice for containers.
	// src: https://docs.kernel.org/admin-guide/cgroup-v2.html (memory.max)
	// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-SHARED-BUFFERS
	effMem, constrained, err := p.OS.EffectiveMemoryBytes()

	if err == osinspect.ErrNoOSFacts {
		if hasSB {
			out = append(out, pgtuner.Advice{
				ID:      "memory.shared-buffers-no-os-facts",
				Level:   pgtuner.Info,
				Section: section,
				Title:   "shared_buffers (no OS facts)",
				Message: fmt.Sprintf("shared_buffers is set to %s. The ratio cannot be computed without total RAM.", mibStr(sharedBufBytes)),
				DocRef:  docMemResource,
			})
		}
		if hasEC {
			out = append(out, pgtuner.Advice{
				ID:      "memory.effective-cache-size-no-os-facts",
				Level:   pgtuner.Info,
				Section: section,
				Title:   "effective_cache_size (no OS facts)",
				Message: fmt.Sprintf("effective_cache_size is set to %s. The planner estimate cannot be verified without total RAM.", mibStr(ecBytes)),
				DocRef:  docMemResource,
			})
		}
		if hasMWM {
			out = append(out, pgtuner.Advice{
				ID:      "memory.maintenance-work-mem-no-os-facts",
				Level:   pgtuner.Info,
				Section: section,
				Title:   "maintenance_work_mem (no OS facts)",
				Message: fmt.Sprintf("maintenance_work_mem is set to %s. It cannot be checked against host RAM.", mibStr(maintWorkMemBytes)),
				DocRef:  docMemResource,
			})
		}
		return out
	}

	total := int64(effMem)
	if total <= 0 {
		// A bogus inspector may return zero.
		return []pgtuner.Advice{{
			ID:      "memory.cannot-evaluate",
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Cannot evaluate memory settings",
			Message: "OS inspector returned effective memory=0. Memory ratio checks are skipped.",
		}}
	}

	// memLabel tells cgroup limits apart from total host RAM in messages.
	memLabel := func() string {
		if constrained {
			return fmt.Sprintf("cgroup memory limit (%s)", mibStr(total))
		}
		return fmt.Sprintf("total RAM (%s)", mibStr(total))
	}

	if hasSB {
		frac := float64(sharedBufBytes) / float64(total)
		switch {
		case frac < sharedBufsLowFrac:
			out = append(out, pgtuner.Advice{
				ID:      "memory.shared-buffers-low",
				Level:   pgtuner.Warn,
				Section: section,
				Title:   "shared_buffers is too low",
				Message: fmt.Sprintf("shared_buffers=%s is %.1f%% of %s. The recommended starting point is 25%%. A shared_buffers value that is too small forces PostgreSQL to rely entirely on the OS page cache.", mibStr(sharedBufBytes), frac*100, memLabel()),
				Detail:  "Set shared_buffers to ~25% of available memory and restart PostgreSQL.",
				DocRef:  docMemResource,
			})
		case frac > sharedBufsHighFrac:
			out = append(out, pgtuner.Advice{
				ID:      "memory.shared-buffers-high",
				Level:   pgtuner.Warn,
				Section: section,
				Title:   "shared_buffers may be too high",
				Message: fmt.Sprintf("shared_buffers=%s is %.1f%% of %s. Values above 40%% yield diminishing returns and leave less memory for the OS page cache and per-backend work.", mibStr(sharedBufBytes), frac*100, memLabel()),
				Detail:  "Consider reducing shared_buffers to 25–40% of available memory.",
				DocRef:  docMemResource,
			})
		default:
			out = append(out, pgtuner.Advice{
				ID:      "memory.shared-buffers-ok",
				Level:   pgtuner.OK,
				Section: section,
				Title:   "shared_buffers is well-sized",
				Message: fmt.Sprintf("shared_buffers=%s (%.1f%% of %s) is within the recommended 15–40%% range.", mibStr(sharedBufBytes), frac*100, memLabel()),
				DocRef:  docMemResource,
			})
		}
	}

	// src: https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-EFFECTIVE-CACHE-SIZE
	if hasEC {
		frac := float64(ecBytes) / float64(total)
		if frac < effectiveCacheLowFrac {
			out = append(out, pgtuner.Advice{
				ID:      "memory.effective-cache-size-low",
				Level:   pgtuner.Info,
				Section: section,
				Title:   "effective_cache_size may be underestimated",
				Message: fmt.Sprintf("effective_cache_size=%s is %.1f%% of %s. The recommended value is 50–75%% to accurately reflect OS + PG cache. An underestimate causes the planner to favour sequential scans over index scans.", mibStr(ecBytes), frac*100, memLabel()),
				Detail:  "Set effective_cache_size to 50–75% of available memory.",
				DocRef:  docMemResource,
			})
		}
	}

	// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-MAINTENANCE-WORK-MEM
	if hasMWM && total > bigHostMinBytes && maintWorkMemBytes < maintWorkMemMinBytes {
		docRef := docMemResource
		pg17note := ""
		if p.Version.AtLeast(17, 0) {
			// PG17 vacuum uses maintenance_work_mem more efficiently via the TID-store.
			// src: https://www.postgresql.org/docs/17/runtime-config-resource.html
			docRef = docMaintenanceWorkMem17
			pg17note = " On PostgreSQL 17+, VACUUM's TID-store uses maintenance_work_mem more efficiently. This setting therefore has an even greater effect."
		}
		out = append(out, pgtuner.Advice{
			ID:      "memory.maintenance-work-mem-low",
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
			ID:      "memory.settings-ok",
			Level:   pgtuner.OK,
			Section: section,
			Title:   "Memory settings look healthy",
			Message: "shared_buffers, effective_cache_size, and maintenance_work_mem are within recommended ranges.",
		})
	}
	return out
}

// mibStr formats bytes as MiB, or GiB from 1024 MiB.
func mibStr(b int64) string {
	mib := float64(b) / (1024 * 1024)
	if mib >= 1024 {
		return fmt.Sprintf("%.1f GiB", mib/1024)
	}
	return fmt.Sprintf("%.0f MiB", mib)
}
