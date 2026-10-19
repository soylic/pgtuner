package checks

import (
	"context"
	"testing"

	"github.com/soylic/pgtuner/internal/osinspect"
	"github.com/soylic/pgtuner/internal/pg"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

func runKernel(f fakeOS) []pgtuner.Advice {
	p := &Probe{
		Settings: pg.NewSettings(nil),
		OS:       f,
		Version:  pg.ServerVersion{Major: 16, Minor: 0},
	}
	return KernelCheck{}.Run(context.Background(), p)
}

// src: https://www.postgresql.org/docs/current/kernel-resources.html

func TestEvalKernel_AllEmpty(t *testing.T) {
	got := evalKernel("", "", "")
	if len(got) != 0 {
		t.Fatalf("expected no advice when all values are empty, got %v", levelsOf(got))
	}
}

func TestEvalKernel_THPAlways_Warn(t *testing.T) {
	got := evalKernel("always", "", "")
	if len(got) == 0 || got[0].Level != pgtuner.Warn {
		t.Fatalf("expected Warn for THP=always, got %v", levelsOf(got))
	}
}

func TestEvalKernel_THPMadvise_NoAdvice(t *testing.T) {
	got := evalKernel("madvise", "", "")
	if len(got) != 0 {
		t.Fatalf("expected no advice for THP=madvise, got %v", levelsOf(got))
	}
}

func TestEvalKernel_THPNever_NoAdvice(t *testing.T) {
	got := evalKernel("never", "", "")
	if len(got) != 0 {
		t.Fatalf("expected no advice for THP=never, got %v", levelsOf(got))
	}
}

func TestEvalKernel_OvercommitNot2_Info(t *testing.T) {
	got := evalKernel("", "0", "")
	if len(got) == 0 || got[0].Level != pgtuner.Info {
		t.Fatalf("expected Info for overcommit=0, got %v", levelsOf(got))
	}
}

func TestEvalKernel_Overcommit1_Info(t *testing.T) {
	got := evalKernel("", "1", "")
	if len(got) == 0 || got[0].Level != pgtuner.Info {
		t.Fatalf("expected Info for overcommit=1, got %v", levelsOf(got))
	}
}

func TestEvalKernel_Overcommit2_NoAdvice(t *testing.T) {
	got := evalKernel("", "2", "")
	if len(got) != 0 {
		t.Fatalf("expected no advice for overcommit=2, got %v", levelsOf(got))
	}
}

func TestEvalKernel_SwappinessHigh_Info(t *testing.T) {
	got := evalKernel("", "", "60")
	if len(got) == 0 || got[0].Level != pgtuner.Info {
		t.Fatalf("expected Info for swappiness=60, got %v", levelsOf(got))
	}
}

func TestEvalKernel_SwappinessAtThreshold_NoAdvice(t *testing.T) {
	got := evalKernel("", "", "10")
	if len(got) != 0 {
		t.Fatalf("expected no advice for swappiness=10 (threshold is >10), got %v", levelsOf(got))
	}
}

func TestEvalKernel_SwappinessLow_NoAdvice(t *testing.T) {
	got := evalKernel("", "", "1")
	if len(got) != 0 {
		t.Fatalf("expected no advice for swappiness=1, got %v", levelsOf(got))
	}
}

func TestEvalKernel_AllBad(t *testing.T) {
	got := evalKernel("always", "0", "60")
	warnCount, infoCount := 0, 0
	for _, a := range got {
		switch a.Level {
		case pgtuner.Warn:
			warnCount++
		case pgtuner.Info:
			infoCount++
		}
	}
	if warnCount == 0 {
		t.Fatalf("expected at least one Warn in combined bad case, got %v", levelsOf(got))
	}
	if infoCount < 2 {
		t.Fatalf("expected at least 2 Info for overcommit+swappiness, got %v", levelsOf(got))
	}
}

func TestKernelCheck_NoOSFacts_SingleInfo(t *testing.T) {
	got := runKernel(fakeOS{}) // all zero → ErrNoOSFacts from Environment()
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 Info advice when no OS facts, got %d: %v", len(got), levelsOf(got))
	}
	if got[0].Level != pgtuner.Info {
		t.Fatalf("expected Info level, got %v", got[0].Level)
	}
}

func TestKernelCheck_THPAlwaysEmitsWarn(t *testing.T) {
	got := runKernel(fakeOS{
		env: osinspect.Environment{Kind: osinspect.EnvBareMetal},
		thp: "always",
		sysctl: map[string]string{
			"vm.overcommit_memory": "2",
			"vm.swappiness":        "1",
		},
	})
	if !hasLevel(got, pgtuner.Warn) {
		t.Fatalf("expected Warn for THP=always via KernelCheck.Run, got %v", levelsOf(got))
	}
}

func TestKernelCheck_AllHealthy_OK(t *testing.T) {
	got := runKernel(fakeOS{
		env: osinspect.Environment{Kind: osinspect.EnvBareMetal},
		thp: "madvise",
		sysctl: map[string]string{
			"vm.overcommit_memory": "2",
			"vm.swappiness":        "1",
		},
	})
	if !hasLevel(got, pgtuner.OK) {
		t.Fatalf("expected OK when all kernel params are healthy, got %v", levelsOf(got))
	}
}

func TestKernelCheck_SysctlUnavailable_Skipped(t *testing.T) {
	// Inspector present but sysctl returns ErrNoOSFacts for all keys (not in map).
	// THP is also empty. Should emit OK because no bad values detected.
	got := runKernel(fakeOS{
		env:    osinspect.Environment{Kind: osinspect.EnvBareMetal},
		thp:    "never",
		sysctl: map[string]string{}, // no sysctl keys → ErrNoOSFacts for each
	})
	if !hasLevel(got, pgtuner.OK) {
		t.Fatalf("expected OK when sysctl unavailable (skipped), got %v", levelsOf(got))
	}
}
