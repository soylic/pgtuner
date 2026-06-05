package checks

import (
	"context"
	"fmt"

	"github.com/OpenRemedy/pgtuner/internal/osinspect"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// EnvironmentCheck reports the detected execution environment (bare-metal, VM,
// container) and the active cgroup version. When a cgroup memory limit
// constrains available RAM below the host total, it emits an advisory so the
// operator knows which memory figure pgtuner is using for ratio calculations.
//
// This check is purely informational — all advice is level Info. It degrades
// gracefully to a single informational message when OS inspection is unavailable
// (i.e. pgtuner is not running on the database host with -os-inspect).
//
// src: https://docs.kernel.org/admin-guide/cgroup-v2.html
// src: https://www.postgresql.org/docs/current/kernel-resources.html
type EnvironmentCheck struct{ clusterScoped }

func (EnvironmentCheck) Name() string { return "environment" }

// Run inspects the execution environment and emits Info advisories. It never
// panics and never returns an error.
func (EnvironmentCheck) Run(_ context.Context, p *Probe) []pgtuner.Advice {
	const section = "Environment"

	env, err := p.OS.Environment()
	if err == osinspect.ErrNoOSFacts {
		return []pgtuner.Advice{{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "Environment detection requires host access",
			Message: "Environment detection requires running pgtuner on the database host (-os-inspect). Without OS inspection, memory and CPU advice uses PostgreSQL settings alone.",
		}}
	}
	if err != nil {
		return []pgtuner.Advice{{
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Environment detection failed",
			Message: fmt.Sprintf("Could not determine execution environment: %v", err),
		}}
	}

	var out []pgtuner.Advice

	// Report the detected environment kind and cgroup version as a baseline
	// informational advisory.
	cgroupDesc := fmt.Sprintf("cgroup v%d", env.CgroupVersion)
	if env.CgroupVersion == 0 {
		cgroupDesc = "cgroup version unknown"
	}
	out = append(out, pgtuner.Advice{
		Level:   pgtuner.Info,
		Section: section,
		Title:   fmt.Sprintf("Running on %s (%s)", env.Kind, cgroupDesc),
		Message: fmt.Sprintf("Detected execution environment: %s with %s. Memory and CPU advice is calibrated to the resources available in this environment.", env.Kind, cgroupDesc),
	})

	// When a cgroup memory limit constrains the process below host RAM, emit a
	// dedicated advisory explaining which memory value pgtuner uses for ratio
	// calculations. This prevents confusing advice like "shared_buffers is
	// too high" when shared_buffers is 25 % of host RAM but 100 % of the
	// cgroup limit.
	// src: https://docs.kernel.org/admin-guide/cgroup-v2.html#memory-interface-files
	effMem, constrained, effErr := p.OS.EffectiveMemoryBytes()
	if effErr == nil && constrained {
		hostMem, hostErr := p.OS.Memory()
		hostGiB := float64(0)
		if hostErr == nil && hostMem.TotalBytes > 0 {
			hostGiB = float64(hostMem.TotalBytes) / (1024 * 1024 * 1024)
		}
		effGiB := float64(effMem) / (1024 * 1024 * 1024)

		var hostDesc string
		if hostGiB > 0 {
			hostDesc = fmt.Sprintf(" (host has %.1f GiB)", hostGiB)
		}
		out = append(out, pgtuner.Advice{
			Level:   pgtuner.Info,
			Section: section,
			Title:   "Running under a cgroup memory limit",
			Message: fmt.Sprintf("Running under a cgroup memory limit of %.1f GiB%s; memory advice uses the effective limit rather than total host RAM.", effGiB, hostDesc),
		})
	}

	return out
}
