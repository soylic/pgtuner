package checks

import (
	"context"
	"fmt"
	"strconv"

	"github.com/soylic/pgtuner/internal/osinspect"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// docKernelResources is the canonical PostgreSQL reference for OS/kernel tuning.
// src: https://www.postgresql.org/docs/current/kernel-resources.html
const docKernelResources = "https://www.postgresql.org/docs/current/kernel-resources.html"

// docLinuxMemOvercommit is the section-level reference for vm.overcommit_memory.
// src: https://www.postgresql.org/docs/current/kernel-resources.html#LINUX-MEMORY-OVERCOMMIT
const docLinuxMemOvercommit = "https://www.postgresql.org/docs/current/kernel-resources.html#LINUX-MEMORY-OVERCOMMIT"

// swappinessHighThreshold is the vm.swappiness value above which pgtuner advises lowering it.
// The Linux default is 60. Community practice for a dedicated server is 1–10.
// src: https://www.postgresql.org/docs/current/kernel-resources.html (community practice)
const swappinessHighThreshold = 10

// evalKernel returns kernel advice from pre-read string values.
// An empty string means the value was unavailable, and that parameter is skipped.
// src: https://www.postgresql.org/docs/current/kernel-resources.html
func evalKernel(thp string, overcommit string, swappiness string) []pgtuner.Advice {
	const section = "OS / Kernel"
	var out []pgtuner.Advice

	// THP "always" can stall queries while the kernel compacts memory. Community practice is "never" or "madvise".
	// src: https://www.postgresql.org/docs/current/kernel-resources.html (huge pages section)
	// src: https://docs.kernel.org/admin-guide/mm/transhuge.html
	switch thp {
	case "always":
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Warn,
			Section: section,
			Title:   "Transparent Huge Pages set to 'always'",
			Message: "transparent_hugepage=always makes the kernel compact memory to form huge pages. This can cause multi-second latency spikes under PostgreSQL workloads. Community practice is to set it to 'never' or 'madvise'.",
			Detail:  "echo never > /sys/kernel/mm/transparent_hugepage/enabled  (add to rc.local to keep the setting after a restart).",
			DocRef:  docKernelResources,
		})
	case "madvise":
		// PostgreSQL opts in explicitly and avoids background compaction. No advice needed.
	}

	// With overcommit_memory 0 or 1 the OOM killer can kill the postmaster.
	// Mode 2 makes allocation failures explicit (ENOMEM at mmap time).
	// src: https://www.postgresql.org/docs/current/kernel-resources.html#LINUX-MEMORY-OVERCOMMIT
	if overcommit != "" && overcommit != "2" {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "vm.overcommit_memory is not set to 2",
			Message: fmt.Sprintf("vm.overcommit_memory=%s. The kernel may let processes promise more virtual memory than is available. The kernel may then start the OOM killer when processes use the pages. On a dedicated PostgreSQL host, the OOM killer can kill the postmaster.", overcommit),
			Detail:  "Set vm.overcommit_memory=2. Set vm.overcommit_ratio (for example 80). Then allocation failures are explicit. Add both settings to /etc/sysctl.conf and run sysctl -p.",
			DocRef:  docLinuxMemOvercommit,
		})
	}

	// High swappiness can swap out hot shared_buffers pages and cause latency spikes.
	// src: https://www.postgresql.org/docs/current/kernel-resources.html (community practice)
	if swappiness != "" {
		if v, err := strconv.Atoi(swappiness); err == nil && v > swappinessHighThreshold {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Info,
				Section: section,
				Title:   "vm.swappiness is high",
				Message: fmt.Sprintf("vm.swappiness=%d. A high value makes the kernel swap anonymous pages (including PostgreSQL working-set memory) to disk, even when free memory is available. This increases query latency. Community practice for dedicated database servers is 1–10.", v),
				Detail:  "Set vm.swappiness=1 in /etc/sysctl.conf. Then run sysctl -p.",
				DocRef:  docKernelResources,
			})
		}
	}

	return out
}

// KernelCheck evaluates THP, vm.overcommit_memory, and vm.swappiness.
// An unavailable sysctl skips only that item.
type KernelCheck struct{ clusterScoped }

func (KernelCheck) Name() string { return "os_kernel" }

// Run reads kernel parameters via the OS inspector.
// Without an inspector (ErrNoOSFacts) it emits a single Info that asks for -os-inspect on the DB host.
func (KernelCheck) Run(_ context.Context, p *Probe) []pgtuner.Advice {
	const section = "OS / Kernel"

	// ErrNoOSFacts means the inspector is a noop. Return one Info instead of three "unavailable" entries.
	_, envErr := p.OS.Environment()
	if envErr == osinspect.ErrNoOSFacts {
		return []pgtuner.Advice{{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "Kernel checks require running on the DB host",
			Message: "pgtuner could not read the OS kernel parameters (THP, vm.overcommit_memory, vm.swappiness). To enable these checks, run pgtuner with -os-inspect on the database host.",
			DocRef:  docKernelResources,
		}}
	}

	thp, _ := p.OS.TransparentHugepages()
	overcommit, _ := p.OS.Sysctl("vm.overcommit_memory")
	swappiness, _ := p.OS.Sysctl("vm.swappiness")

	out := evalKernel(thp, overcommit, swappiness)

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.OK,
			Section: section,
			Title:   "Kernel parameters look healthy",
			Message: "Transparent Huge Pages, vm.overcommit_memory, and vm.swappiness are in the recommended ranges for a PostgreSQL host.",
			DocRef:  docKernelResources,
		})
	}
	return out
}
