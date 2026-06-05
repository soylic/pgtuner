package checks

import (
	"context"
	"fmt"

	"github.com/OpenRemedy/pgtuner/internal/osinspect"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// docHugePages is the canonical reference for huge pages on Linux.
// src: https://www.postgresql.org/docs/current/kernel-resources.html#LINUX-HUGE-PAGES
const docHugePages = "https://www.postgresql.org/docs/current/kernel-resources.html#LINUX-HUGE-PAGES"

// HugePagesCheck evaluates whether huge pages are correctly sized for
// PostgreSQL's shared memory area.
type HugePagesCheck struct{ clusterScoped }

func (HugePagesCheck) Name() string { return "hugepages" }

// Run checks huge-pages configuration. It never panics and never returns
// an error. When OS facts are unavailable it emits Info rather than skipping.
func (HugePagesCheck) Run(_ context.Context, p *Probe) []pgtuner.Advice {
	const section = "Huge Pages"
	var out []pgtuner.Advice

	// --- huge_pages GUC -------------------------------------------------------
	// huge_pages = "try" means PostgreSQL will silently fall back to no huge
	// pages if the allocation fails; operators may not notice the fallback.
	// "on" makes failure explicit at startup.
	// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-HUGE-PAGES
	if hp, ok := p.Settings.Get("huge_pages"); ok && hp == "try" {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "huge_pages=try may silently disable huge pages",
			Message: "huge_pages is set to 'try'; PostgreSQL will fall back to regular pages without error if huge pages are unavailable. Set to 'on' (once sizing is confirmed correct) to make failures visible at startup.",
			DocRef:  docHugePages,
		})
	}

	// --- container environment note -------------------------------------------
	// vm.nr_hugepages is a host-wide kernel parameter. Inside an unprivileged
	// container (Docker, Kubernetes, LXC) it cannot be changed — the operator
	// must configure huge pages on the container host or Kubernetes node instead.
	// Emit an Info so the reader knows where to look when the advice below shows
	// "insufficient huge pages".
	// src: https://www.postgresql.org/docs/current/kernel-resources.html#LINUX-HUGE-PAGES
	// src: https://docs.kernel.org/admin-guide/mm/hugetlbpage.html (host-global)
	if env, envErr := p.OS.Environment(); envErr == nil {
		switch env.Kind {
		case osinspect.EnvDocker, osinspect.EnvKubernetes, osinspect.EnvLXC:
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Info,
				Section: section,
				Title:   "vm.nr_hugepages is host-global — configure on the host/node",
				Message: fmt.Sprintf("Running inside a %s container: vm.nr_hugepages is a host-global kernel parameter and usually cannot be set from inside an unprivileged container. Configure huge pages on the container host (or Kubernetes node) rather than inside this container.", env.Kind),
				DocRef:  docHugePages,
			})
		}
	}
	// Degrade gracefully when Environment() errors: skip this sub-check.

	// --- check whether enough huge pages are allocated -----------------------
	mem, err := p.OS.Memory()
	if err == osinspect.ErrNoOSFacts {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "Cannot verify huge pages without host access",
			Message: "OS inspector is not available; run pgtuner with a local OS inspector to verify vm.nr_hugepages is sufficient for PostgreSQL's shared memory area.",
			DocRef:  docHugePages,
		})
		return out
	}

	if mem.HugePageSize == 0 {
		// No huge pages configured on the kernel side; nothing to verify.
		if len(out) == 0 {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.OK,
				Section: section,
				Title:   "Huge pages not in use",
				Message: "HugePageSize=0 reported by OS; huge pages are not configured. No action needed unless you wish to enable them.",
				DocRef:  docHugePages,
			})
		}
		return out
	}

	// Determine how many huge pages PostgreSQL needs.
	var neededPages int64
	var neededSource string

	if p.Version.AtLeast(15, 0) {
		// PG15+ exposes shared_memory_size_in_huge_pages: an exact count of huge
		// pages needed for the shared memory segment.
		// src: https://www.postgresql.org/docs/15/runtime-config-preset.html
		n, ok := p.Settings.Int("shared_memory_size_in_huge_pages")
		if ok && n > 0 {
			neededPages = n
			neededSource = "shared_memory_size_in_huge_pages GUC"
		}
	} else {
		// PG < 15: estimate from shared_buffers (8kB blocks → bytes) divided by
		// HugePageSize. This underestimates total shared memory (which also
		// includes lock tables, WAL buffers, etc.) but is a reasonable floor.
		// src: https://www.postgresql.org/docs/current/kernel-resources.html#LINUX-HUGE-PAGES
		sbBlocks, ok := p.Settings.Int("shared_buffers")
		if ok && sbBlocks > 0 {
			sbBytes := sbBlocks * 8192
			neededPages = (sbBytes + int64(mem.HugePageSize) - 1) / int64(mem.HugePageSize) // ceiling division
			neededSource = fmt.Sprintf("shared_buffers estimate (%d MiB / %d kB huge pages)", sbBytes/(1024*1024), mem.HugePageSize/1024)
		}
	}

	if neededPages > 0 && int64(mem.HugePagesTotal) < neededPages {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "Insufficient huge pages configured",
			Message: fmt.Sprintf("PostgreSQL needs %d huge pages (from %s) but vm.nr_hugepages=%d. PostgreSQL will fall back to regular pages, forfeiting TLB performance benefits.", neededPages, neededSource, mem.HugePagesTotal),
			Detail:  fmt.Sprintf("Set vm.nr_hugepages >= %d in /etc/sysctl.conf and run sysctl -p. Add some headroom (e.g. +10%%) for kernel and other uses.", neededPages),
			DocRef:  docHugePages,
		})
	} else if neededPages > 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.OK,
			Section: section,
			Title:   "Huge pages look adequate",
			Message: fmt.Sprintf("vm.nr_hugepages=%d is sufficient for PostgreSQL's estimated need of %d huge pages (from %s).", mem.HugePagesTotal, neededPages, neededSource),
			DocRef:  docHugePages,
		})
	}

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.OK,
			Section: section,
			Title:   "Huge pages configuration looks healthy",
			Message: "Huge pages are configured and appear sufficient for PostgreSQL's shared memory area.",
			DocRef:  docHugePages,
		})
	}
	return out
}
