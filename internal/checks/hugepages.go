package checks

import (
	"context"
	"fmt"

	"github.com/soylic/pgtuner/internal/osinspect"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

const docHugePages = "https://www.postgresql.org/docs/current/kernel-resources.html#LINUX-HUGE-PAGES"

// HugePagesCheck evaluates whether huge pages fit PostgreSQL's shared memory area.
type HugePagesCheck struct{ clusterScoped }

func (HugePagesCheck) Name() string { return "hugepages" }

// Run emits Info when OS facts are unavailable. It never panics and never returns an error.
func (HugePagesCheck) Run(_ context.Context, p *Probe) []pgtuner.Advice {
	const section = "Huge Pages"
	var out []pgtuner.Advice

	// "try" falls back to regular pages silently. "on" fails at startup instead.
	// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-HUGE-PAGES
	if hp, ok := p.Settings.Get("huge_pages"); ok && hp == "try" {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "huge_pages=try may silently disable huge pages",
			Message: "huge_pages is set to 'try'. If huge pages are unavailable, PostgreSQL falls back to regular pages without error. Set it to 'on' (once sizing is confirmed correct) to make failures visible at startup.",
			DocRef:  docHugePages,
		})
	}

	// vm.nr_hugepages is host-wide. An unprivileged container (Docker, Kubernetes, LXC)
	// cannot change it, so this Info points the reader to the host or node.
	// src: https://www.postgresql.org/docs/current/kernel-resources.html#LINUX-HUGE-PAGES
	// src: https://docs.kernel.org/admin-guide/mm/hugetlbpage.html (host-global)
	if env, envErr := p.OS.Environment(); envErr == nil {
		switch env.Kind {
		case osinspect.EnvDocker, osinspect.EnvKubernetes, osinspect.EnvLXC:
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Info,
				Section: section,
				Title:   "vm.nr_hugepages is host-global: configure on the host/node",
				Message: fmt.Sprintf("Running inside a %s container. vm.nr_hugepages is a host-global kernel parameter. You usually cannot set it from inside an unprivileged container. Configure huge pages on the container host (or Kubernetes node) instead of inside this container.", env.Kind),
				DocRef:  docHugePages,
			})
		}
	}
	// An Environment() error skips the container note.

	mem, err := p.OS.Memory()
	if err == osinspect.ErrNoOSFacts {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "Cannot verify huge pages without host access",
			Message: "OS inspector is not available. Run pgtuner with a local OS inspector to verify that vm.nr_hugepages is sufficient for PostgreSQL's shared memory area.",
			DocRef:  docHugePages,
		})
		return out
	}

	if mem.HugePageSize == 0 {
		if len(out) == 0 {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.OK,
				Section: section,
				Title:   "Huge pages not in use",
				Message: "HugePageSize=0 reported by OS. Huge pages are not configured. No action is needed unless you want to enable them.",
				DocRef:  docHugePages,
			})
		}
		return out
	}

	var neededPages int64
	var neededSource string

	if p.Version.AtLeast(15, 0) {
		// PG15+ exposes the exact count in shared_memory_size_in_huge_pages.
		// src: https://www.postgresql.org/docs/15/runtime-config-preset.html
		n, ok := p.Settings.Int("shared_memory_size_in_huge_pages")
		if ok && n > 0 {
			neededPages = n
			neededSource = "shared_memory_size_in_huge_pages GUC"
		}
	} else {
		// PG<15: shared_buffers / HugePageSize is a floor. It ignores lock tables, WAL buffers, etc.
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
			Message: fmt.Sprintf("PostgreSQL needs %d huge pages (from %s) but vm.nr_hugepages=%d. PostgreSQL will fall back to regular pages. This forfeits the TLB performance benefits.", neededPages, neededSource, mem.HugePagesTotal),
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
