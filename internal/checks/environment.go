package checks

import (
	"context"
	"fmt"

	"github.com/soylic/pgtuner/internal/osinspect"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// EnvironmentCheck reports the execution environment (bare-metal, VM, container)
// and the cgroup version. When a cgroup limit puts RAM below the host total, it
// states which memory figure pgtuner uses for ratios.
// All advice is Info. Without OS inspection (-os-inspect on the database host) it emits one message.
// src: https://docs.kernel.org/admin-guide/cgroup-v2.html
// src: https://www.postgresql.org/docs/current/kernel-resources.html
type EnvironmentCheck struct{ clusterScoped }

func (EnvironmentCheck) Name() string { return "environment" }

// Run never panics and never returns an error.
func (EnvironmentCheck) Run(_ context.Context, p *Probe) []pgtuner.Advice {
	const section = "Environment"

	env, err := p.OS.Environment()
	if err == osinspect.ErrNoOSFacts {
		return []pgtuner.Advice{{
			ID:      "environment.host-access-required",
			Level:   pgtuner.Info,
			Section: section,
			Title:   "Environment detection requires host access",
			Message: "Environment detection requires running pgtuner on the database host (-os-inspect). Without OS inspection, memory and CPU advice uses PostgreSQL settings alone.",
		}}
	}
	if err != nil {
		return []pgtuner.Advice{{
			ID:      "environment.detection-failed",
			Level:   pgtuner.Unknown,
			Section: section,
			Title:   "Environment detection failed",
			Message: fmt.Sprintf("Could not determine execution environment: %v", err),
		}}
	}

	var out []pgtuner.Advice

	cgroupDesc := fmt.Sprintf("cgroup v%d", env.CgroupVersion)
	if env.CgroupVersion == 0 {
		cgroupDesc = "cgroup version unknown"
	}
	out = append(out, pgtuner.Advice{
		ID:      "environment.detected",
		Level:   pgtuner.Info,
		Section: section,
		Title:   fmt.Sprintf("Running on %s (%s)", env.Kind, cgroupDesc),
		Message: fmt.Sprintf("Detected execution environment: %s with %s. Memory and CPU advice uses the resources available in this environment.", env.Kind, cgroupDesc),
	})

	// Without this note, advice such as "shared_buffers is too high" confuses when
	// shared_buffers is 25 % of host RAM but 100 % of the cgroup limit.
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
			ID:      "environment.cgroup-memory-limit",
			Level:   pgtuner.Info,
			Section: section,
			Title:   "Running under a cgroup memory limit",
			Message: fmt.Sprintf("Running under a cgroup memory limit of %.1f GiB%s. Memory advice uses the effective limit instead of total host RAM.", effGiB, hostDesc),
		})
	}

	return out
}
