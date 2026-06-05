package checks

import (
	"context"
	"fmt"
	"strconv"

	"github.com/OpenRemedy/pgtuner/internal/osinspect"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// docKernelResources is the canonical PostgreSQL reference for OS/kernel tuning.
// src: https://www.postgresql.org/docs/current/kernel-resources.html
const docKernelResources = "https://www.postgresql.org/docs/current/kernel-resources.html"

// docLinuxMemOvercommit is the section-level reference for vm.overcommit_memory.
// src: https://www.postgresql.org/docs/current/kernel-resources.html#LINUX-MEMORY-OVERCOMMIT
const docLinuxMemOvercommit = "https://www.postgresql.org/docs/current/kernel-resources.html#LINUX-MEMORY-OVERCOMMIT"

// swappinessHighThreshold is the vm.swappiness value above which pgtuner advises
// lowering it. The Linux default is 60; for a dedicated PostgreSQL server the
// community recommends 1–10 to keep the working set resident.
// This is community practice documented in numerous PostgreSQL DBA guides and
// consistent with the kernel docs on vm.swappiness semantics.
// src: https://www.postgresql.org/docs/current/kernel-resources.html (community practice)
const swappinessHighThreshold = 10

// evalKernel is the pure decision function for OS kernel parameter advice.
// Each parameter is passed as a pre-read string value; an empty string means
// the value was unavailable (e.g. sysctl error) — that parameter is skipped.
//
// Parameters:
//   - thp: TransparentHugepages result ("always", "madvise", "never", or "")
//   - overcommit: vm.overcommit_memory value ("0", "1", "2", or "")
//   - swappiness: vm.swappiness value (decimal integer string, or "")
//
// Returns zero or more Advice entries; never panics.
//
// src: https://www.postgresql.org/docs/current/kernel-resources.html
func evalKernel(thp string, overcommit string, swappiness string) []pgtuner.Advice {
	const section = "OS / Kernel"
	var out []pgtuner.Advice

	// --- Transparent Huge Pages -----------------------------------------------
	// THP in "always" mode can cause multi-second latency spikes when the kernel
	// compacts memory to form huge pages. PostgreSQL accesses shared memory in
	// many small operations; unexpected compaction stalls show as query latency
	// spikes. Community practice (and Postgres documentation) recommends "never"
	// or "madvise" for database servers.
	// This is community practice; the kernel docs describe THP behaviour.
	// src: https://www.postgresql.org/docs/current/kernel-resources.html (huge pages section)
	// src: https://docs.kernel.org/admin-guide/mm/transhuge.html
	switch thp {
	case "always":
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Warn,
			Section: section,
			Title:   "Transparent Huge Pages set to 'always'",
			Message: "transparent_hugepage=always causes the kernel to compact memory to form huge pages, producing multi-second latency spikes under PostgreSQL workloads. Community practice recommends setting it to 'never' or 'madvise'.",
			Detail:  "echo never > /sys/kernel/mm/transparent_hugepage/enabled  (add to rc.local for persistence).",
			DocRef:  docKernelResources,
		})
	case "madvise":
		// madvise lets PostgreSQL opt in to huge pages for its shared memory area
		// explicitly; random background compaction is avoided. Acceptable.
		// No advisory needed.
	}
	// "never" or empty: skip.

	// --- vm.overcommit_memory -------------------------------------------------
	// With overcommit_memory=0 (default) or 1, the kernel may allow more memory
	// to be promised than is available and invoke the OOM killer when actual
	// pages are faulted. On a dedicated PostgreSQL host this risks killing the
	// postmaster. Setting overcommit_memory=2 + tuning overcommit_ratio makes
	// allocation failures explicit (ENOMEM at mmap time) rather than silent.
	// src: https://www.postgresql.org/docs/current/kernel-resources.html#LINUX-MEMORY-OVERCOMMIT
	if overcommit != "" && overcommit != "2" {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "vm.overcommit_memory is not set to 2",
			Message: fmt.Sprintf("vm.overcommit_memory=%s; the kernel may allow processes to promise more virtual memory than is available and invoke the OOM killer when pages are actually faulted. On a dedicated PostgreSQL host this risks killing the postmaster.", overcommit),
			Detail:  "Set vm.overcommit_memory=2 and tune vm.overcommit_ratio (e.g. 80) so allocation failures are explicit. Add to /etc/sysctl.conf and run sysctl -p.",
			DocRef:  docLinuxMemOvercommit,
		})
	}

	// --- vm.swappiness --------------------------------------------------------
	// A high vm.swappiness encourages the kernel to swap anonymous pages to disk
	// even when free memory is available. For a database server this can evict
	// hot shared_buffers or working-set pages, causing sudden query latency.
	// The recommended value for a dedicated PostgreSQL host is 1–10.
	// This is community practice (not a formal PostgreSQL doc section, but
	// consistent with kernel documentation on swappiness semantics).
	// src: https://www.postgresql.org/docs/current/kernel-resources.html (community practice)
	if swappiness != "" {
		if v, err := strconv.Atoi(swappiness); err == nil && v > swappinessHighThreshold {
			out = append(out, pgtuner.Advice{
				Level:   pgtuner.Info,
				Section: section,
				Title:   "vm.swappiness is high",
				Message: fmt.Sprintf("vm.swappiness=%d; a high value encourages the kernel to swap anonymous pages (including PostgreSQL working-set memory) even when free memory is available, increasing query latency. Community practice recommends 1–10 for dedicated database servers.", v),
				Detail:  "Set vm.swappiness=1 in /etc/sysctl.conf and run sysctl -p.",
				DocRef:  docKernelResources,
			})
		}
	}

	return out
}

// KernelCheck evaluates Linux kernel parameters relevant to PostgreSQL
// performance: Transparent Huge Pages, vm.overcommit_memory, and vm.swappiness.
// All sub-checks degrade gracefully: when a sysctl is unavailable that item is
// skipped and the rest still run.
type KernelCheck struct{ clusterScoped }

func (KernelCheck) Name() string { return "os_kernel" }

// Run reads kernel parameters via the OS inspector and returns advice. It never
// panics and never returns an error. When the inspector itself is absent
// (ErrNoOSFacts on the first required probe) it emits a single Info explaining
// that kernel checks require running on the DB host with -os-inspect.
func (KernelCheck) Run(_ context.Context, p *Probe) []pgtuner.Advice {
	const section = "OS / Kernel"

	// Probe the environment first. If we get ErrNoOSFacts the inspector is the
	// noop and we cannot read any sysctl values either — emit a single Info and
	// return early rather than producing three identical "unavailable" entries.
	_, envErr := p.OS.Environment()
	if envErr == osinspect.ErrNoOSFacts {
		return []pgtuner.Advice{{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "Kernel checks require running on the DB host",
			Message: "OS kernel parameters (THP, vm.overcommit_memory, vm.swappiness) could not be read. Run pgtuner with -os-inspect on the database host to enable these checks.",
			DocRef:  docKernelResources,
		}}
	}

	// Read each sysctl independently; a failure on one does not skip the others.
	thp, _ := p.OS.TransparentHugepages()
	overcommit, _ := p.OS.Sysctl("vm.overcommit_memory")
	swappiness, _ := p.OS.Sysctl("vm.swappiness")

	out := evalKernel(thp, overcommit, swappiness)

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.OK,
			Section: section,
			Title:   "Kernel parameters look healthy",
			Message: "Transparent Huge Pages, vm.overcommit_memory, and vm.swappiness are within recommended ranges for a PostgreSQL host.",
			DocRef:  docKernelResources,
		})
	}
	return out
}
