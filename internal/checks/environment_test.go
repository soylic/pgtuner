package checks

import (
	"context"
	"strings"
	"testing"

	"github.com/OpenRemedy/pgtuner/internal/osinspect"
	"github.com/OpenRemedy/pgtuner/internal/pg"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

func runEnvironment(os osinspect.Inspector) []pgtuner.Advice {
	p := &Probe{
		Settings: pg.NewSettings(nil),
		OS:       os,
	}
	return EnvironmentCheck{}.Run(context.Background(), p)
}

// TestEnvironmentNoOSFacts: Noop inspector → exactly one Info advisory telling
// the user to run pgtuner on the database host.
func TestEnvironmentNoOSFacts(t *testing.T) {
	got := runEnvironment(osinspect.NoopInspector{})

	if len(got) != 1 {
		t.Fatalf("expected 1 advice for Noop, got %d: %v", len(got), got)
	}
	a := got[0]
	if a.Level != pgtuner.Info {
		t.Errorf("level=%v want Info", a.Level)
	}
	if !strings.Contains(strings.ToLower(a.Message), "os-inspect") &&
		!strings.Contains(strings.ToLower(a.Message), "-os-inspect") {
		t.Errorf("message should mention -os-inspect flag, got %q", a.Message)
	}
}

// TestEnvironmentBareMetalNoConstraint: bare-metal host with no cgroup memory
// constraint → Info mentioning the kind; no constrained-memory advisory.
func TestEnvironmentBareMetalNoConstraint(t *testing.T) {
	const hostMem = 16 * 1024 * 1024 * 1024 // 16 GiB
	got := runEnvironment(fakeOS{
		env:    osinspect.Environment{Kind: osinspect.EnvBareMetal, CgroupVersion: 2},
		effMem: hostMem, // not constrained (effMemConstrained defaults to false)
		mem:    osinspect.Memory{TotalBytes: hostMem},
	})

	// Must contain at least one Info about the environment kind.
	found := false
	for _, a := range got {
		if a.Level == pgtuner.Info && strings.Contains(strings.ToLower(a.Message+a.Title), "bare-metal") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Info mentioning bare-metal, got %v", got)
	}

	// Must NOT contain a constrained-memory advisory.
	for _, a := range got {
		if strings.Contains(a.Message, "cgroup memory limit") {
			t.Errorf("unexpected constrained-memory advisory on bare-metal host: %v", a.Message)
		}
	}
}

// TestEnvironmentContainerConstrainedMemory: Docker container with cgroup
// memory limit less than host RAM → Info about the effective limit.
func TestEnvironmentContainerConstrainedMemory(t *testing.T) {
	const hostMem = 32 * 1024 * 1024 * 1024  // 32 GiB host
	const cgroupMem = 4 * 1024 * 1024 * 1024  // 4 GiB cgroup limit
	got := runEnvironment(fakeOS{
		env:               osinspect.Environment{Kind: osinspect.EnvDocker, CgroupVersion: 2},
		effMem:            cgroupMem,
		effMemConstrained: true,
		mem:               osinspect.Memory{TotalBytes: hostMem},
	})

	// Must have an Info mentioning the cgroup limit.
	found := false
	for _, a := range got {
		if a.Level == pgtuner.Info && strings.Contains(a.Message, "cgroup memory limit") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected constrained-memory Info, got %v", got)
	}
}

// TestEnvironmentKubernetes: Kubernetes pod environment → Info mentioning kind.
func TestEnvironmentKubernetes(t *testing.T) {
	const hostMem = 8 * 1024 * 1024 * 1024
	got := runEnvironment(fakeOS{
		env:    osinspect.Environment{Kind: osinspect.EnvKubernetes, CgroupVersion: 2},
		effMem: hostMem,
		mem:    osinspect.Memory{TotalBytes: hostMem},
	})

	found := false
	for _, a := range got {
		if a.Level == pgtuner.Info && strings.Contains(strings.ToLower(a.Message+a.Title), "kubernetes") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Info mentioning kubernetes, got %v", got)
	}
}
