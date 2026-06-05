package checks

import (
	"context"
	"fmt"

	"github.com/OpenRemedy/pgtuner/internal/osinspect"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// docPlannerRuntime is the canonical reference for query planner cost GUCs.
// src: https://www.postgresql.org/docs/current/runtime-config-query.html
const docPlannerRuntime = "https://www.postgresql.org/docs/current/runtime-config-query.html"

// docRandomPageCost is the section-level reference for random_page_cost.
// src: https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-RANDOM-PAGE-COST
const docRandomPageCost = "https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-RANDOM-PAGE-COST"

// docEffectiveIOConcurrency is the section-level reference for effective_io_concurrency.
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-EFFECTIVE-IO-CONCURRENCY
const docEffectiveIOConcurrency = "https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-EFFECTIVE-IO-CONCURRENCY"

// randomPageCostSSDThreshold is the value above which random_page_cost is
// flagged as probably too high for SSD storage. For SSDs the random vs
// sequential cost difference is much smaller; values near 1.1 are recommended.
// The default of 4.0 was calibrated for spinning-disk seek times.
// src: https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-RANDOM-PAGE-COST
const randomPageCostSSDThreshold = 1.5

// effectiveIOConcurrencyLowThreshold is the threshold below which
// effective_io_concurrency is flagged as too conservative for SSDs/RAID.
// PostgreSQL default is 1 (safe for single spinning disk); SSDs and RAID
// arrays benefit from values like 200 (NVMe SSDs often go higher).
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-EFFECTIVE-IO-CONCURRENCY
const effectiveIOConcurrencyLowThreshold = 1

// defaultStatisticsTargetLow is the minimum recommended value for
// default_statistics_target. The PostgreSQL default of 100 is a reasonable
// baseline; lower values cause the planner to have fewer histogram buckets
// and less accurate row-count estimates on skewed data.
// src: https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-DEFAULT-STATISTICS-TARGET
const defaultStatisticsTargetLow = 100

// evalRandomPageCostStorage is the pure decision function for storage-aware
// random_page_cost advice. It takes:
//   - rotational: nil if storage type is unknown, true if all devices are HDD,
//     false if any device is non-rotational (SSD/NVMe).
//   - rpc: the configured random_page_cost value.
//
// Returns zero or one Advice describing whether the value is appropriate for
// the detected storage type. An empty slice means no issue.
//
// src: https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-RANDOM-PAGE-COST
func evalRandomPageCostStorage(rotational *bool, rpc float64) *pgtuner.Advice {
	if rotational == nil {
		// Storage type unknown: fall back to the generic threshold check.
		if rpc > randomPageCostSSDThreshold {
			a := pgtuner.Advice{
				Level:   pgtuner.Info,
				Section: "Planner",
				Title:   "random_page_cost may be too high for SSD storage",
				Message: fmt.Sprintf("random_page_cost=%.2f; on SSD storage random and sequential I/O costs are much closer. A high value biases the planner toward sequential scans over index scans.", rpc),
				Detail:  "If this instance runs on SSDs or NVMe, consider lowering random_page_cost to 1.1–1.5. Also consider pairing with effective_io_concurrency=200.",
				DocRef:  docRandomPageCost,
			}
			return &a
		}
		return nil
	}

	if !*rotational {
		// Non-rotational (SSD/NVMe) detected.
		// src: https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-RANDOM-PAGE-COST
		// For SSDs random I/O is nearly as fast as sequential; a random_page_cost
		// much above 1.0 causes the planner to over-prefer sequential scans.
		// Community-recommended value: 1.1.
		if rpc > randomPageCostSSDThreshold {
			a := pgtuner.Advice{
				Level:   pgtuner.Info,
				Section: "Planner",
				Title:   "random_page_cost is high for detected non-rotational storage",
				Message: fmt.Sprintf("random_page_cost=%.2f; non-rotational storage (SSD/NVMe) was detected. On SSDs random I/O latency is nearly as low as sequential; keeping random_page_cost high causes the planner to favour sequential scans over index scans unnecessarily.", rpc),
				Detail:  "Lower random_page_cost toward 1.1 for SSD/NVMe storage. Also consider raising effective_io_concurrency to 200 or higher.",
				DocRef:  docRandomPageCost,
			}
			return &a
		}
		return nil
	}

	// Rotational (HDD) detected.
	// On spinning disks the seek penalty makes random I/O significantly more
	// expensive than sequential; random_page_cost should be closer to 4.0.
	// A value below 2.0 is likely too aggressive and will cause the planner to
	// choose index scans that are slower than sequential scans on HDDs.
	// src: https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-RANDOM-PAGE-COST
	if rpc < 2.0 {
		a := pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: "Planner",
			Title:   "random_page_cost may be too low for spinning-disk storage",
			Message: fmt.Sprintf("random_page_cost=%.2f; spinning-disk (HDD) storage was detected. On HDDs random seeks are much more expensive than sequential reads; a low random_page_cost causes the planner to choose index scans over cheaper sequential scans.", rpc),
			Detail:  "For HDD storage, set random_page_cost to 2.0–4.0 to match the actual random/sequential I/O cost ratio.",
			DocRef:  docRandomPageCost,
		}
		return &a
	}
	return nil
}

// storageRotational inspects a slice of StorageDevices and returns:
//   - nil: no devices or unable to determine.
//   - *false: at least one device is non-rotational (SSD/NVMe takes precedence).
//   - *true: all devices are rotational (HDD).
func storageRotational(devices []osinspect.StorageDevice) *bool {
	if len(devices) == 0 {
		return nil
	}
	for _, d := range devices {
		if !d.Rotational {
			// Any non-rotational device present: treat the whole deployment as SSD.
			f := false
			return &f
		}
	}
	t := true
	return &t
}

// PlannerCheck evaluates query planner cost and statistics settings.
type PlannerCheck struct{ clusterScoped }

func (PlannerCheck) Name() string { return "planner" }

// Run checks planner-related GUCs and returns advice. It never panics and
// never returns an error; it is settings-only (no DB query).
func (PlannerCheck) Run(_ context.Context, p *Probe) []pgtuner.Advice {
	const section = "Planner"
	var out []pgtuner.Advice

	// --- random_page_cost (storage-aware) --------------------------------------
	// Detect storage type via OS inspector and emit specific advice for SSD vs
	// HDD vs unknown. Falls back to the generic threshold check when storage
	// info is unavailable.
	// src: https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-RANDOM-PAGE-COST
	if rpc, ok := p.Settings.Float("random_page_cost"); ok {
		devices, devErr := p.OS.StorageDevices()
		var rotational *bool
		if devErr == nil {
			rotational = storageRotational(devices)
		}
		// devErr == ErrNoOSFacts or any other error → rotational stays nil → generic advice.
		if a := evalRandomPageCostStorage(rotational, rpc); a != nil {
			out = append(out, *a)
		}
	}

	// --- effective_io_concurrency -----------------------------------------------
	// Default is 1 (safe for a single spinning disk). SSDs and RAID controllers
	// can service many concurrent I/O requests; a value of 1 prevents PostgreSQL
	// from issuing prefetch hints to the kernel.
	// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-EFFECTIVE-IO-CONCURRENCY
	if eic, ok := p.Settings.Int("effective_io_concurrency"); ok && eic <= effectiveIOConcurrencyLowThreshold {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "effective_io_concurrency is low",
			Message: fmt.Sprintf("effective_io_concurrency=%d; on SSD/RAID storage PostgreSQL can issue more concurrent I/O requests. A value of 1 limits bitmap-heap-scan prefetching.", eic),
			Detail:  "On SSDs raise effective_io_concurrency to 200; on NVMe or RAID-10 even higher values (e.g. 500–1000) may help. Set to 0 only on single spinning disks.",
			DocRef:  docEffectiveIOConcurrency,
		})
	}

	// --- default_statistics_target ---------------------------------------------
	// The planner relies on per-column histograms; fewer buckets (lower target)
	// mean coarser estimates, especially for skewed distributions.
	// src: https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-DEFAULT-STATISTICS-TARGET
	if dst, ok := p.Settings.Int("default_statistics_target"); ok && dst < defaultStatisticsTargetLow {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "default_statistics_target is below the recommended default",
			Message: fmt.Sprintf("default_statistics_target=%d is below the PostgreSQL default of 100. Fewer histogram buckets produce less accurate row-count estimates, potentially leading to poor plan choices on skewed columns.", dst),
			Detail:  "Raise default_statistics_target to at least 100. For tables with heavily skewed distributions consider ALTER TABLE … ALTER COLUMN … SET STATISTICS <n> with higher per-column targets.",
			DocRef:  docPlannerRuntime,
		})
	}

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.OK,
			Section: section,
			Title:   "Planner settings look healthy",
			Message: "random_page_cost, effective_io_concurrency, and default_statistics_target are within recommended ranges.",
			DocRef:  docPlannerRuntime,
		})
	}
	return out
}
