// Package osinspect provides pluggable OS-level inspection (memory, sysctl)
// used to enrich tuning advice. Implementations: LocalInspector (reads the
// local host) and NoopInspector (no facts available).
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

// EnvKind identifies the execution environment the process is running in.
// Detection is best-effort and based on signals from /proc, /sys, and environment
// variables as documented by the Linux kernel cgroup/namespace subsystem and
// container runtime conventions.
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

// Environment describes the execution context: what kind of host (bare-metal,
// VM, container) and which cgroup API version is active.
// CgroupVersion is 1, 2, or 0 when it cannot be determined.
type Environment struct {
	Kind          EnvKind
	CgroupVersion int // 1, 2, or 0 if unknown
}

// StorageDevice holds basic block-device information for a mounted filesystem.
// Detection is best-effort; callers must tolerate empty slices.
type StorageDevice struct {
	Device     string
	Rotational bool   // true = spinning disk (HDD), false = SSD / NVMe
	FSType     string
	MountOpts  string
}

// Inspector reads OS-level facts. Implementations must be safe to call when
// the underlying source is unavailable, returning an error rather than panicking.
type Inspector interface {
	Memory() (Memory, error)
	Sysctl(key string) (string, error) // e.g. "vm.overcommit_memory"

	// Environment returns the detected execution environment (container type,
	// cgroup version). Best-effort; returns EnvUnknown on detection failure.
	// src: https://docs.kernel.org/admin-guide/cgroup-v2.html
	Environment() (Environment, error)

	// EffectiveMemoryBytes returns the memory budget applicable to processes in
	// this environment. When a cgroup memory limit constrains memory below the
	// host's total RAM, it returns (limit, true, nil). Otherwise it returns
	// (hostMemTotal, false, nil). Callers should use this value — rather than
	// Memory().TotalBytes — for sizing ratios, so container-limited deployments
	// receive correct advice.
	// src: https://docs.kernel.org/admin-guide/cgroup-v2.html (memory.max)
	EffectiveMemoryBytes() (limit uint64, constrained bool, err error)

	// EffectiveCPUs returns the CPU budget available to this process. When a
	// cgroup CPU quota is set and is less than the host's logical CPU count, it
	// returns (quota/period, true, nil). Otherwise it returns
	// (float64(runtime.NumCPU()), false, nil).
	// src: https://docs.kernel.org/admin-guide/cgroup-v2.html (cpu.max)
	EffectiveCPUs() (cpus float64, constrained bool, err error)

	// TransparentHugepages returns the active THP mode: "always", "madvise", or
	// "never". The value is the bracketed entry from
	// /sys/kernel/mm/transparent_hugepage/enabled, e.g. "always [madvise] never"
	// returns "madvise".
	// src: https://docs.kernel.org/admin-guide/mm/transhuge.html
	TransparentHugepages() (string, error)

	// StorageDevices returns block-device metadata for the filesystems pgtuner
	// cares about (at minimum the root mount). Detection is best-effort; an
	// empty slice with a nil error is valid when devices cannot be determined.
	StorageDevices() ([]StorageDevice, error)
}
