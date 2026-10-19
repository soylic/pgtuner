package checks

import (
	"context"
	"fmt"

	"github.com/soylic/pgtuner/internal/osinspect"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// src: https://www.postgresql.org/docs/current/runtime-config-query.html
const docPlannerRuntime = "https://www.postgresql.org/docs/current/runtime-config-query.html"

// src: https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-RANDOM-PAGE-COST
const docRandomPageCost = "https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-RANDOM-PAGE-COST"

// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-EFFECTIVE-IO-CONCURRENCY
const docEffectiveIOConcurrency = "https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-EFFECTIVE-IO-CONCURRENCY"

// randomPageCostSSDThreshold is the random_page_cost above which SSD storage gets advice.
// The default of 4.0 assumes spinning-disk seek times. Values near 1.1 suit SSDs.
// src: https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-RANDOM-PAGE-COST
const randomPageCostSSDThreshold = 1.5

// effectiveIOConcurrencyLowThreshold is the effective_io_concurrency at or below which advice fires.
// The default of 1 suits a single spinning disk. SSDs and RAID benefit from about 200.
// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-EFFECTIVE-IO-CONCURRENCY
const effectiveIOConcurrencyLowThreshold = 1

// defaultStatisticsTargetLow is the minimum recommended default_statistics_target (the PostgreSQL default).
// Lower values give fewer histogram buckets and worse row estimates on skewed data.
// src: https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-DEFAULT-STATISTICS-TARGET
const defaultStatisticsTargetLow = 100

// evalRandomPageCostStorage returns advice for random_page_cost, or nil when the value fits.
// rotational is nil if the storage type is unknown, true if all devices are HDD,
// and false if any device is non-rotational.
// src: https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-RANDOM-PAGE-COST
func evalRandomPageCostStorage(rotational *bool, rpc float64) *pgtuner.Advice {
	if rotational == nil {
		if rpc > randomPageCostSSDThreshold {
			a := pgtuner.Advice{
				Level:   pgtuner.Info,
				Section: "Planner",
				Title:   "random_page_cost may be too high for SSD storage",
				Message: fmt.Sprintf("random_page_cost=%.2f. On SSD storage the costs of random and sequential I/O are much closer. A high value makes the planner prefer sequential scans over index scans.", rpc),
				Detail:  "If this instance runs on SSDs or NVMe, consider a lower random_page_cost of 1.1–1.5. Consider also effective_io_concurrency=200.",
				DocRef:  docRandomPageCost,
			}
			return &a
		}
		return nil
	}

	if !*rotational {
		// On SSDs a random_page_cost far above 1.0 over-favours sequential scans. Community value: 1.1.
		// src: https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-RANDOM-PAGE-COST
		if rpc > randomPageCostSSDThreshold {
			a := pgtuner.Advice{
				Level:   pgtuner.Info,
				Section: "Planner",
				Title:   "random_page_cost is high for detected non-rotational storage",
				Message: fmt.Sprintf("random_page_cost=%.2f. pgtuner detected non-rotational storage (SSD/NVMe). On SSDs the latency of random I/O is nearly as low as sequential I/O. A high random_page_cost makes the planner prefer sequential scans over index scans without need.", rpc),
				Detail:  "For SSD/NVMe storage, lower random_page_cost toward 1.1. Consider also a higher effective_io_concurrency of 200 or more.",
				DocRef:  docRandomPageCost,
			}
			return &a
		}
		return nil
	}

	// On HDDs random_page_cost should be near 4.0. Below 2.0 the planner picks index scans that are slower than sequential scans.
	// src: https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-RANDOM-PAGE-COST
	if rpc < 2.0 {
		a := pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: "Planner",
			Title:   "random_page_cost may be too low for spinning-disk storage",
			Message: fmt.Sprintf("random_page_cost=%.2f. pgtuner detected spinning-disk (HDD) storage. On HDDs random seeks cost much more than sequential reads. A low random_page_cost makes the planner choose index scans instead of cheaper sequential scans.", rpc),
			Detail:  "For HDD storage, set random_page_cost to 2.0–4.0. This matches the real cost ratio of random to sequential I/O.",
			DocRef:  docRandomPageCost,
		}
		return &a
	}
	return nil
}

// storageRotational returns nil for no devices, false if any device is non-rotational, and true if all are rotational.
func storageRotational(devices []osinspect.StorageDevice) *bool {
	if len(devices) == 0 {
		return nil
	}
	for _, d := range devices {
		if !d.Rotational {
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

// Run checks planner GUCs. It reads settings only and runs no DB query.
func (PlannerCheck) Run(_ context.Context, p *Probe) []pgtuner.Advice {
	const section = "Planner"
	var out []pgtuner.Advice

	// Storage-aware advice. Without storage info, the generic threshold applies.
	// src: https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-RANDOM-PAGE-COST
	if rpc, ok := p.Settings.Float("random_page_cost"); ok {
		devices, devErr := p.OS.StorageDevices()
		var rotational *bool
		if devErr == nil {
			rotational = storageRotational(devices)
		}
		// Any devErr leaves rotational nil and gives generic advice.
		if a := evalRandomPageCostStorage(rotational, rpc); a != nil {
			out = append(out, *a)
		}
	}

	// A value of 1 stops PostgreSQL from issuing prefetch hints to SSDs and RAID controllers.
	// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-EFFECTIVE-IO-CONCURRENCY
	if eic, ok := p.Settings.Int("effective_io_concurrency"); ok && eic <= effectiveIOConcurrencyLowThreshold {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "effective_io_concurrency is low",
			Message: fmt.Sprintf("effective_io_concurrency=%d. On SSD/RAID storage PostgreSQL can issue more concurrent I/O requests. A value of 1 limits bitmap-heap-scan prefetching.", eic),
			Detail:  "On SSDs, set effective_io_concurrency to 200. On NVMe or RAID-10, higher values (for example 500–1000) may help. Set it to 0 only on single spinning disks.",
			DocRef:  docEffectiveIOConcurrency,
		})
	}

	// src: https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-DEFAULT-STATISTICS-TARGET
	if dst, ok := p.Settings.Int("default_statistics_target"); ok && dst < defaultStatisticsTargetLow {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "default_statistics_target is below the recommended default",
			Message: fmt.Sprintf("default_statistics_target=%d is below the PostgreSQL default of 100. Fewer histogram buckets give less accurate row-count estimates. This can lead to poor plan choices on skewed columns.", dst),
			Detail:  "Set default_statistics_target to at least 100. For tables with heavily skewed distributions, consider ALTER TABLE … ALTER COLUMN … SET STATISTICS <n> with higher per-column targets.",
			DocRef:  docPlannerRuntime,
		})
	}

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.OK,
			Section: section,
			Title:   "Planner settings look healthy",
			Message: "random_page_cost, effective_io_concurrency, and default_statistics_target are in the recommended ranges.",
			DocRef:  docPlannerRuntime,
		})
	}
	return out
}
