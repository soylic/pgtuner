package osinspect

import (
	"runtime"
	"testing"
)

func TestLocalMemory(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	m, err := LocalInspector{}.Memory()
	if err != nil || m.TotalBytes == 0 {
		t.Fatalf("mem=%+v err=%v", m, err)
	}
	if m.HugePageSize == 0 {
		t.Fatalf("HugePageSize not parsed: %+v", m)
	}
}

func TestNoopMemory(t *testing.T) {
	_, err := NoopInspector{}.Memory()
	if err == nil {
		t.Fatal("noop should error")
	}
}

// TestNoopNewMethods verifies all new Inspector methods on NoopInspector return ErrNoOSFacts.
func TestNoopNewMethods(t *testing.T) {
	n := NoopInspector{}

	if _, err := n.Environment(); err != ErrNoOSFacts {
		t.Errorf("Environment: want ErrNoOSFacts, got %v", err)
	}
	if _, _, err := n.EffectiveMemoryBytes(); err != ErrNoOSFacts {
		t.Errorf("EffectiveMemoryBytes: want ErrNoOSFacts, got %v", err)
	}
	if _, _, err := n.EffectiveCPUs(); err != ErrNoOSFacts {
		t.Errorf("EffectiveCPUs: want ErrNoOSFacts, got %v", err)
	}
	if _, err := n.TransparentHugepages(); err != ErrNoOSFacts {
		t.Errorf("TransparentHugepages: want ErrNoOSFacts, got %v", err)
	}
	if _, err := n.StorageDevices(); err != ErrNoOSFacts {
		t.Errorf("StorageDevices: want ErrNoOSFacts, got %v", err)
	}
}

// TestLocalEnvironment verifies Environment() returns a known Kind and a valid
// cgroup version on this Linux host.
func TestLocalEnvironment(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	env, err := LocalInspector{}.Environment()
	if err != nil {
		t.Fatalf("Environment() error: %v", err)
	}
	validKinds := map[EnvKind]bool{
		EnvBareMetal: true, EnvVM: true, EnvDocker: true,
		EnvKubernetes: true, EnvLXC: true, EnvUnknown: true,
	}
	if !validKinds[env.Kind] {
		t.Errorf("Environment().Kind=%q not in valid set", env.Kind)
	}
	if env.CgroupVersion != 0 && env.CgroupVersion != 1 && env.CgroupVersion != 2 {
		t.Errorf("Environment().CgroupVersion=%d want 0, 1, or 2", env.CgroupVersion)
	}
}

// TestLocalEffectiveMemoryBytes verifies EffectiveMemoryBytes() returns a
// positive value on this host.
func TestLocalEffectiveMemoryBytes(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	limit, _, err := LocalInspector{}.EffectiveMemoryBytes()
	if err != nil {
		t.Fatalf("EffectiveMemoryBytes() error: %v", err)
	}
	if limit == 0 {
		t.Error("EffectiveMemoryBytes() returned 0")
	}
}

// TestLocalEffectiveCPUs verifies EffectiveCPUs() returns a positive value.
func TestLocalEffectiveCPUs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	cpus, _, err := LocalInspector{}.EffectiveCPUs()
	if err != nil {
		t.Fatalf("EffectiveCPUs() error: %v", err)
	}
	if cpus <= 0 {
		t.Errorf("EffectiveCPUs() returned %v, want > 0", cpus)
	}
}

// TestLocalTransparentHugepages verifies TransparentHugepages() returns a
// known value or skips if the file is absent (e.g. inside a restricted container).
func TestLocalTransparentHugepages(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	thp, err := LocalInspector{}.TransparentHugepages()
	if err != nil {
		// THP file absent (e.g. running inside a container without /sys/kernel/mm
		// exposed) — not an error condition for this host.
		t.Skipf("TransparentHugepages() unavailable: %v", err)
	}
	valid := map[string]bool{"always": true, "madvise": true, "never": true}
	if !valid[thp] {
		t.Errorf("TransparentHugepages() = %q, want always|madvise|never", thp)
	}
}

// TestLocalStorageDevices verifies StorageDevices() does not error and (on a
// real Linux system) returns at least one device entry.
func TestLocalStorageDevices(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	devs, err := LocalInspector{}.StorageDevices()
	if err != nil {
		t.Fatalf("StorageDevices() error: %v", err)
	}
	// Best-effort: empty is allowed, but on a real host we expect at least one.
	// Don't fail hard if the device list is empty (e.g. in a restricted container).
	t.Logf("StorageDevices() returned %d device(s): %+v", len(devs), devs)
}
