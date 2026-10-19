// Package osinspect provides pluggable OS-level inspection (memory, sysctl)
// used by tuning advice. LocalInspector reads the local host. NoopInspector returns no facts.
package osinspect

import "errors"

// ErrNoOSFacts is returned when no OS inspector is available.
var ErrNoOSFacts = errors.New("no OS inspector available")

// Memory holds host memory facts. Huge-page counts are page counts, not bytes.
type Memory struct {
	TotalBytes     uint64
	HugePageSize   uint64 // bytes per huge page
	HugePagesTotal uint64 // number of huge pages configured
	HugePagesFree  uint64 // number of huge pages free
}

// EnvKind identifies the execution environment. Detection is best-effort and
// uses signals from /proc, /sys, and environment variables.
// src: https://docs.kernel.org/admin-guide/cgroup-v2.html
// src: https://www.postgresql.org/docs/current/kernel-resources.html
type EnvKind string

const (
	EnvBareMetal  EnvKind = "bare-metal"
	EnvVM         EnvKind = "vm"
	EnvDocker     EnvKind = "docker"
	EnvKubernetes EnvKind = "kubernetes"
	EnvLXC        EnvKind = "lxc"
	EnvUnknown    EnvKind = "unknown"
)

// Environment describes the host kind and the active cgroup version.
type Environment struct {
	Kind          EnvKind
	CgroupVersion int // 1, 2, or 0 if unknown
}

// StorageDevice describes the block device of a mounted filesystem.
// Detection is best-effort. Callers must accept empty slices.
type StorageDevice struct {
	Device     string
	Rotational bool // true = spinning disk (HDD), false = SSD / NVMe
	FSType     string
	MountOpts  string
}

// Inspector reads OS-level facts. When the source is unavailable, it returns an error and does not panic.
type Inspector interface {
	Memory() (Memory, error)
	Sysctl(key string) (string, error) // e.g. "vm.overcommit_memory"

	// Environment returns the detected environment. Best-effort: EnvUnknown on failure.
	// src: https://docs.kernel.org/admin-guide/cgroup-v2.html
	Environment() (Environment, error)

	// EffectiveMemoryBytes returns (limit, true, nil) when a cgroup limit is below
	// host RAM, else (hostMemTotal, false, nil). Use it instead of Memory().TotalBytes
	// for sizing ratios so container limits are respected.
	// src: https://docs.kernel.org/admin-guide/cgroup-v2.html (memory.max)
	EffectiveMemoryBytes() (limit uint64, constrained bool, err error)

	// EffectiveCPUs returns (quota/period, true, nil) when a cgroup CPU quota is
	// below the logical CPU count, else (float64(runtime.NumCPU()), false, nil).
	// src: https://docs.kernel.org/admin-guide/cgroup-v2.html (cpu.max)
	EffectiveCPUs() (cpus float64, constrained bool, err error)

	// TransparentHugepages returns the active THP mode: "always", "madvise", or "never".
	// It is the bracketed entry in /sys/kernel/mm/transparent_hugepage/enabled.
	// src: https://docs.kernel.org/admin-guide/mm/transhuge.html
	TransparentHugepages() (string, error)

	// StorageDevices returns block devices for the relevant filesystems (at least the
	// root mount). An empty slice with a nil error is valid.
	StorageDevices() ([]StorageDevice, error)
}
