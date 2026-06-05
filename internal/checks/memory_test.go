package checks

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/OpenRemedy/pgtuner/internal/osinspect"
	"github.com/OpenRemedy/pgtuner/internal/pg"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// helper: build Probe for memory checks.
func runMemory(settings map[string]string, os osinspect.Inspector, major, minor int) []pgtuner.Advice {
	p := &Probe{
		Settings: pg.NewSettings(settings),
		OS:       os,
		Version:  pg.ServerVersion{Major: major, Minor: minor},
	}
	return MemoryCheck{}.Run(context.Background(), p)
}

// pg_settings stores shared_buffers/effective_cache_size in 8kB units.
// 1 GiB = 1048576 kB / 8 = 131072 blocks of 8kB.
const (
	blocks8kPerGiB = 131072  // 1 GiB in 8kB blocks
	kbPerGiB       = 1048576 // 1 GiB in kB (for maintenance_work_mem)
)

// bigRAMBytes is 16 GiB expressed as a uint64 for use with effMem.
const bigRAMBytes = 16 * 1024 * 1024 * 1024

// fakeOSWithRAM returns a fakeOS configured with the given unconstrained
// effective memory. This is the standard test helper for memory checks.
func fakeOSWithRAM(bytes uint64) fakeOS {
	return fakeOS{effMem: bytes, effMemConstrained: false}
}

// TestMemorySharedBufsLow: shared_buffers at 10% of 16 GiB (1.6 GiB) → Warn.
func TestMemorySharedBufsLow(t *testing.T) {
	// 10% of 16 GiB = 1.6 GiB = ~209715 blocks of 8kB (truncated to int)
	sbBlocks := int64(blocks8kPerGiB * 16 / 10) // integer arithmetic avoids float conversion
	got := runMemory(map[string]string{
		"shared_buffers":       fmt.Sprint(sbBlocks),
		"effective_cache_size": fmt.Sprint(blocks8kPerGiB * 12), // 12 GiB, 75%
		"maintenance_work_mem": fmt.Sprint(kbPerGiB),            // 1 GiB, fine
	}, fakeOSWithRAM(bigRAMBytes), 16, 0)

	if !hasLevel(got, pgtuner.Warn) {
		t.Fatalf("expected Warn for low shared_buffers, got %v", levelsOf(got))
	}
}

// TestMemorySharedBufsHigh: shared_buffers at 50% of 16 GiB → Warn.
func TestMemorySharedBufsHigh(t *testing.T) {
	sbBlocks := int64(blocks8kPerGiB * 8) // 8 GiB = 50% of 16 GiB
	got := runMemory(map[string]string{
		"shared_buffers":       fmt.Sprint(sbBlocks),
		"effective_cache_size": fmt.Sprint(blocks8kPerGiB * 10),
		"maintenance_work_mem": fmt.Sprint(kbPerGiB),
	}, fakeOSWithRAM(bigRAMBytes), 16, 0)

	if !hasLevel(got, pgtuner.Warn) {
		t.Fatalf("expected Warn for high shared_buffers, got %v", levelsOf(got))
	}
}

// TestMemorySharedBufsOK: shared_buffers at 25% → OK.
func TestMemorySharedBufsOK(t *testing.T) {
	sbBlocks := int64(blocks8kPerGiB * 4) // 4 GiB = 25% of 16 GiB
	got := runMemory(map[string]string{
		"shared_buffers":       fmt.Sprint(sbBlocks),
		"effective_cache_size": fmt.Sprint(blocks8kPerGiB * 10), // 62.5%
		"maintenance_work_mem": fmt.Sprint(kbPerGiB),
	}, fakeOSWithRAM(bigRAMBytes), 16, 0)

	if !hasLevel(got, pgtuner.OK) {
		t.Fatalf("expected OK for in-range shared_buffers, got %v", levelsOf(got))
	}
}

// TestMemoryMaintenanceWorkMemLow: 32 MiB on 16 GiB host → Info.
func TestMemoryMaintenanceWorkMemLow(t *testing.T) {
	sbBlocks := int64(blocks8kPerGiB * 4) // 25% — fine
	got := runMemory(map[string]string{
		"shared_buffers":       fmt.Sprint(sbBlocks),
		"effective_cache_size": fmt.Sprint(blocks8kPerGiB * 10),
		"maintenance_work_mem": "32768", // 32 MiB in kB
	}, fakeOSWithRAM(bigRAMBytes), 16, 0)

	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for low maintenance_work_mem on big host, got %v", levelsOf(got))
	}
}

// TestMemoryMaintenanceWorkMemPG17Note: low mwm on PG17 should mention TID-store.
func TestMemoryMaintenanceWorkMemPG17Note(t *testing.T) {
	sbBlocks := int64(blocks8kPerGiB * 4)
	got := runMemory(map[string]string{
		"shared_buffers":       fmt.Sprint(sbBlocks),
		"effective_cache_size": fmt.Sprint(blocks8kPerGiB * 10),
		"maintenance_work_mem": "32768", // 32 MiB in kB
	}, fakeOSWithRAM(bigRAMBytes), 17, 0)

	found := false
	for _, a := range got {
		if a.Level == pgtuner.Info && strings.Contains(a.Message, "TID-store") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Info with TID-store mention for PG17, got %v", got)
	}
}

// TestMemoryNoOSFacts: when OS facts unavailable, emit Info for each setting (no Warn/Bad).
func TestMemoryNoOSFacts(t *testing.T) {
	got := runMemory(map[string]string{
		"shared_buffers":       fmt.Sprint(blocks8kPerGiB * 4),
		"effective_cache_size": fmt.Sprint(blocks8kPerGiB * 10),
		"maintenance_work_mem": "262144",
	}, fakeOS{memErr: osinspect.ErrNoOSFacts}, 16, 0)

	for _, a := range got {
		if a.Level == pgtuner.Warn || a.Level == pgtuner.Bad {
			t.Fatalf("expected only Info when OS facts unavailable, got level=%v in %v", a.Level, got)
		}
	}
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info advice when OS facts unavailable, got %v", levelsOf(got))
	}
}

// TestMemoryConstrainedCgroupMessage: when memory is cgroup-constrained the advice
// message must say "cgroup memory limit" instead of "total RAM".
// src: https://docs.kernel.org/admin-guide/cgroup-v2.html (memory.max)
func TestMemoryConstrainedCgroupMessage(t *testing.T) {
	// 4 GiB cgroup limit; shared_buffers at 10% (too low) so we get a Warn.
	const cgroupLimitBytes = 4 * 1024 * 1024 * 1024
	sbBlocks := int64(blocks8kPerGiB * 4 / 10) // ~10% of 4 GiB
	got := runMemory(map[string]string{
		"shared_buffers":       fmt.Sprint(sbBlocks),
		"effective_cache_size": fmt.Sprint(blocks8kPerGiB * 3), // 75% of 4 GiB — fine
		"maintenance_work_mem": fmt.Sprint(kbPerGiB),           // 1 GiB — fine
	}, fakeOS{effMem: cgroupLimitBytes, effMemConstrained: true}, 16, 0)

	if !hasLevel(got, pgtuner.Warn) {
		t.Fatalf("expected Warn for low shared_buffers under cgroup limit, got %v", levelsOf(got))
	}
	found := false
	for _, a := range got {
		if a.Level == pgtuner.Warn && strings.Contains(a.Message, "cgroup memory limit") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Warn message containing 'cgroup memory limit' for constrained path, got %v", got)
	}
}

// TestMemoryUnconstrainedMessage: non-constrained path should say "total RAM".
func TestMemoryUnconstrainedMessage(t *testing.T) {
	sbBlocks := int64(blocks8kPerGiB * 4 / 10) // ~10% of 16 GiB → Warn
	got := runMemory(map[string]string{
		"shared_buffers":       fmt.Sprint(sbBlocks),
		"effective_cache_size": fmt.Sprint(blocks8kPerGiB * 10),
		"maintenance_work_mem": fmt.Sprint(kbPerGiB),
	}, fakeOSWithRAM(bigRAMBytes), 16, 0)

	if !hasLevel(got, pgtuner.Warn) {
		t.Fatalf("expected Warn for low shared_buffers, got %v", levelsOf(got))
	}
	found := false
	for _, a := range got {
		if a.Level == pgtuner.Warn && strings.Contains(a.Message, "total RAM") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Warn message containing 'total RAM' for unconstrained path, got %v", got)
	}
}
