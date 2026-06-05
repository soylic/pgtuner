package osinspect

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// LocalInspector reads OS facts from the local host's /proc filesystem.
// Intended to run on the database host itself.
type LocalInspector struct{}

func (LocalInspector) Memory() (Memory, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return Memory{}, err
	}
	defer f.Close()
	var m Memory
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		// /proc/meminfo sizes are in kB; HugePages_* are raw counts.
		switch fields[0] {
		case "MemTotal:":
			kb, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return Memory{}, fmt.Errorf("parse MemTotal: %w", err)
			}
			m.TotalBytes = kb * 1024
		case "Hugepagesize:":
			kb, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return Memory{}, fmt.Errorf("parse Hugepagesize: %w", err)
			}
			m.HugePageSize = kb * 1024
		case "HugePages_Total:":
			m.HugePagesTotal, _ = strconv.ParseUint(fields[1], 10, 64)
		case "HugePages_Free:":
			m.HugePagesFree, _ = strconv.ParseUint(fields[1], 10, 64)
		}
	}
	if err := sc.Err(); err != nil {
		return Memory{}, err
	}
	if m.TotalBytes == 0 {
		return Memory{}, fmt.Errorf("MemTotal not found in /proc/meminfo")
	}
	return m, nil
}

func (LocalInspector) Sysctl(key string) (string, error) {
	if strings.ContainsAny(key, "/\\") {
		return "", fmt.Errorf("sysctl key %q: must not contain path separators", key)
	}
	p := filepath.Join("/proc/sys", strings.ReplaceAll(key, ".", "/"))
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// detectCgroupVersion returns the cgroup API version active on the host.
// It checks for the cgroup v2 unified hierarchy first (a well-known file that
// is present on all v2 mounts), then falls back to the v1 memory controller.
// src: https://docs.kernel.org/admin-guide/cgroup-v2.html
//   - cgroup v2: /sys/fs/cgroup/cgroup.controllers exists
//   - cgroup v1: /sys/fs/cgroup/memory exists (the memory sub-hierarchy)
func detectCgroupVersion() int {
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err == nil {
		return 2
	}
	if _, err := os.Stat("/sys/fs/cgroup/memory"); err == nil {
		return 1
	}
	return 0
}

// detectEnvKind determines the execution environment using signals from
// /proc, /sys, and environment variables. The precedence order is:
//  1. Kubernetes — KUBERNETES_SERVICE_HOST set or cgroup path contains "kubepods".
//  2. Docker — /.dockerenv present or cgroup path contains "docker".
//  3. LXC — /proc/1/environ contains "lxc" or cgroup path contains "lxc".
//  4. VM — /sys/class/dmi/id/product_name matches a known hypervisor string.
//  5. BareMetal — none of the above matched.
//
// src: https://docs.kernel.org/admin-guide/cgroup-v2.html (cgroup paths)
// src: https://www.postgresql.org/docs/current/kernel-resources.html
func detectEnvKind() EnvKind {
	cgroupContent := readCgroupContent()

	// --- Kubernetes -----------------------------------------------------------
	// The kubelet injects KUBERNETES_SERVICE_HOST into every pod's environment.
	// Additionally, Kubernetes cgroup names always start with "kubepods".
	// src: https://kubernetes.io/docs/concepts/services-networking/connect-applications-service/
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return EnvKubernetes
	}
	if strings.Contains(cgroupContent, "kubepods") {
		return EnvKubernetes
	}

	// --- Docker ---------------------------------------------------------------
	// Docker creates /.dockerenv in every container it starts.
	// src: https://docs.docker.com/engine/reference/run/
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return EnvDocker
	}
	if strings.Contains(cgroupContent, "docker") {
		return EnvDocker
	}

	// --- LXC ------------------------------------------------------------------
	// LXC sets container=lxc in PID 1's environment and uses cgroup paths
	// containing "lxc".
	if strings.Contains(cgroupContent, "/lxc/") || strings.Contains(cgroupContent, "lxc-") {
		return EnvLXC
	}
	if lxcInEnviron() {
		return EnvLXC
	}

	// --- VM -------------------------------------------------------------------
	// Read the DMI product name exposed by the kernel via sysfs. Known
	// hypervisor strings are from the SMBIOS vendor table and well-established
	// practice in cloud-detection libraries.
	// src: https://www.kernel.org/doc/html/latest/admin-guide/acpi/index.html
	// src: https://cloud-hypervisor.azurewebsites.net/ (product name conventions)
	dmiName := readFileString("/sys/class/dmi/id/product_name")
	dmiUpper := strings.ToUpper(dmiName)
	knownHypervisors := []string{
		"KVM", "VMWARE", "VIRTUALBOX", "VBOX",
		"MICROSOFT CORPORATION", // Hyper-V
		"XEN", "QEMU",
		"AMAZON EC2", "HVM DOMU", // AWS / Xen
	}
	for _, h := range knownHypervisors {
		if strings.Contains(dmiUpper, h) {
			return EnvVM
		}
	}

	return EnvBareMetal
}

// readCgroupContent reads /proc/1/cgroup (the init process's cgroup membership)
// which reveals container hierarchy names. Returns empty string on failure.
// src: https://docs.kernel.org/admin-guide/cgroup-v2.html
func readCgroupContent() string {
	b, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return ""
	}
	return string(b)
}

// lxcInEnviron checks whether /proc/1/environ contains "container=lxc".
// Best-effort: returns false on permission or parse error.
func lxcInEnviron() bool {
	b, err := os.ReadFile("/proc/1/environ")
	if err != nil {
		return false
	}
	// environ is NUL-delimited key=value pairs.
	for _, entry := range bytes.Split(b, []byte{0}) {
		if string(entry) == "container=lxc" {
			return true
		}
	}
	return false
}

// readFileString reads a file and returns its trimmed content, or "" on error.
func readFileString(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Environment returns the detected execution environment. Detection is
// best-effort and never fails — unknown conditions return EnvUnknown with nil.
// src: https://docs.kernel.org/admin-guide/cgroup-v2.html
// src: https://www.postgresql.org/docs/current/kernel-resources.html
func (LocalInspector) Environment() (Environment, error) {
	return Environment{
		Kind:          detectEnvKind(),
		CgroupVersion: detectCgroupVersion(),
	}, nil
}

// EffectiveMemoryBytes returns the memory limit applicable to processes in
// this cgroup hierarchy. When the cgroup limit constrains memory below the
// host total, it returns (limit, true, nil). Otherwise it returns
// (hostMemTotal, false, nil).
//
// Cgroup v2: reads /sys/fs/cgroup/memory.max. The sentinel value "max"
// means unlimited.
// src: https://docs.kernel.org/admin-guide/cgroup-v2.html#memory-interface-files
//
// Cgroup v1: reads /sys/fs/cgroup/memory/memory.limit_in_bytes. A value
// near math.MaxInt64 (the kernel uses 0x7FFFFFFFFFFFF000) means unlimited.
// src: https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v1/memory.html
func (li LocalInspector) EffectiveMemoryBytes() (uint64, bool, error) {
	hostMem, err := li.Memory()
	if err != nil {
		return 0, false, err
	}
	hostTotal := hostMem.TotalBytes

	switch detectCgroupVersion() {
	case 2:
		raw := readFileString("/sys/fs/cgroup/memory.max")
		if raw == "" || raw == "max" {
			// "max" means no limit — use host RAM.
			return hostTotal, false, nil
		}
		limit, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			// Unparseable → fall back to host total.
			return hostTotal, false, nil
		}
		if limit < hostTotal {
			return limit, true, nil
		}
		return hostTotal, false, nil

	case 1:
		raw := readFileString("/sys/fs/cgroup/memory/memory.limit_in_bytes")
		if raw == "" {
			return hostTotal, false, nil
		}
		limit, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return hostTotal, false, nil
		}
		// The kernel sets the unlimited sentinel to a page-aligned value just
		// below 2^63 (typically 0x7FFFFFFFFFFFF000 = 9223372036854771712 bytes).
		// Any limit >= half of 2^63 is treated as "unlimited".
		// src: https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v1/memory.html
		const cgroupV1Unlimited = uint64(1<<63 - 1)
		if limit >= cgroupV1Unlimited/2 {
			return hostTotal, false, nil
		}
		if limit < hostTotal {
			return limit, true, nil
		}
		return hostTotal, false, nil
	}

	// No cgroup hierarchy recognised — use host total.
	return hostTotal, false, nil
}

// EffectiveCPUs returns the CPU quota available to this process. When a
// cgroup CPU quota is configured and resolves to fewer CPUs than the host
// provides, it returns (quota/period, true, nil). Otherwise it returns
// (float64(runtime.NumCPU()), false, nil).
//
// Cgroup v2: reads /sys/fs/cgroup/cpu.max, format "<quota> <period>" where
// quota may be "max" (unlimited).
// src: https://docs.kernel.org/admin-guide/cgroup-v2.html#cpu-interface-files
//
// Cgroup v1: reads /sys/fs/cgroup/cpu/cpu.cfs_quota_us and
// /sys/fs/cgroup/cpu/cpu.cfs_period_us. A quota of -1 means unlimited.
// src: https://www.kernel.org/doc/html/latest/scheduler/sched-bwc.html
func (LocalInspector) EffectiveCPUs() (float64, bool, error) {
	hostCPUs := float64(runtime.NumCPU())

	switch detectCgroupVersion() {
	case 2:
		raw := readFileString("/sys/fs/cgroup/cpu.max")
		if raw == "" {
			return hostCPUs, false, nil
		}
		parts := strings.Fields(raw)
		if len(parts) != 2 || parts[0] == "max" {
			return hostCPUs, false, nil
		}
		quota, err := strconv.ParseFloat(parts[0], 64)
		if err != nil || quota <= 0 {
			return hostCPUs, false, nil
		}
		period, err := strconv.ParseFloat(parts[1], 64)
		if err != nil || period <= 0 {
			return hostCPUs, false, nil
		}
		cpus := quota / period
		if cpus < hostCPUs {
			return cpus, true, nil
		}
		return hostCPUs, false, nil

	case 1:
		quotaRaw := readFileString("/sys/fs/cgroup/cpu/cpu.cfs_quota_us")
		periodRaw := readFileString("/sys/fs/cgroup/cpu/cpu.cfs_period_us")
		if quotaRaw == "" || periodRaw == "" {
			return hostCPUs, false, nil
		}
		quota, err := strconv.ParseInt(quotaRaw, 10, 64)
		if err != nil || quota <= 0 {
			// quota == -1 means unlimited.
			return hostCPUs, false, nil
		}
		period, err := strconv.ParseInt(periodRaw, 10, 64)
		if err != nil || period <= 0 {
			return hostCPUs, false, nil
		}
		cpus := float64(quota) / float64(period)
		if cpus < hostCPUs {
			return cpus, true, nil
		}
		return hostCPUs, false, nil
	}

	return hostCPUs, false, nil
}

// TransparentHugepages reads the active THP policy from the kernel's sysfs
// interface and returns the bracketed active mode: "always", "madvise", or
// "never". The file format is e.g. "always [madvise] never".
// src: https://docs.kernel.org/admin-guide/mm/transhuge.html
func (LocalInspector) TransparentHugepages() (string, error) {
	const thpPath = "/sys/kernel/mm/transparent_hugepage/enabled"
	raw := readFileString(thpPath)
	if raw == "" {
		return "", fmt.Errorf("transparent hugepage file unavailable: %s", thpPath)
	}
	// Parse the bracketed active value, e.g. "always [madvise] never".
	start := strings.Index(raw, "[")
	end := strings.Index(raw, "]")
	if start < 0 || end < 0 || end <= start {
		return "", fmt.Errorf("unrecognised THP format: %q", raw)
	}
	return raw[start+1 : end], nil
}

// StorageDevices returns block-device information for filesystems pgtuner
// cares about. It parses /proc/mounts to find the root mount ("/") and reads
// /sys/block/<device>/queue/rotational to determine if the device is spinning.
// This is best-effort: an empty slice with nil error is returned whenever the
// device cannot be determined.
//
// src: /proc/mounts format — https://man7.org/linux/man-pages/man5/fstab.5.html
// src: /sys/block rotational — https://www.kernel.org/doc/html/latest/block/queue-sysfs.html
func (LocalInspector) StorageDevices() ([]StorageDevice, error) {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return nil, nil //nolint:nilerr // best-effort
	}
	defer f.Close()

	// We want the most-specific mount that covers "/" — the last entry in
	// /proc/mounts that has mount-point "/".
	var rootDev, rootFS, rootOpts string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		device, mountpoint, fstype, opts := fields[0], fields[1], fields[2], fields[3]
		if mountpoint == "/" {
			rootDev = device
			rootFS = fstype
			rootOpts = opts
		}
	}
	if err := sc.Err(); err != nil {
		return nil, nil //nolint:nilerr // best-effort
	}
	if rootDev == "" {
		return nil, nil
	}

	sd := StorageDevice{
		Device:    rootDev,
		FSType:    rootFS,
		MountOpts: rootOpts,
	}

	// Resolve the base device name (strip partition number) to look up sysfs.
	// e.g. /dev/sda1 → sda, /dev/nvme0n1p1 → nvme0n1
	base := filepath.Base(rootDev)
	rotPath := "/sys/block/" + base + "/queue/rotational"
	if _, err := os.Stat(rotPath); os.IsNotExist(err) {
		// Try stripping the trailing digit(s) for partition names like sda1 → sda.
		// For nvme: nvme0n1p1 → nvme0n1 (strip "p" + digits suffix).
		base = stripPartitionSuffix(base)
		rotPath = "/sys/block/" + base + "/queue/rotational"
	}
	rotRaw := readFileString(rotPath)
	if rotRaw == "1" {
		sd.Rotational = true
	}
	// rotRaw == "0" → SSD/NVMe, sd.Rotational stays false (the zero value).

	return []StorageDevice{sd}, nil
}

// stripPartitionSuffix strips a trailing partition number from a block device
// name so /dev/sda1 → sda, /dev/nvme0n1p1 → nvme0n1.
// Only the simple cases needed for the root-device lookup are handled.
func stripPartitionSuffix(dev string) string {
	// nvme / mmcblk style: ends in "p<digits>" — strip the "p<digits>" suffix.
	for i := len(dev) - 1; i >= 0; i-- {
		if dev[i] < '0' || dev[i] > '9' {
			if i < len(dev)-1 && dev[i] == 'p' {
				// Potential nvme partition suffix: strip "p<digits>".
				return dev[:i]
			}
			break
		}
	}
	// sd/vd style: ends in digit(s) — strip trailing digits.
	i := len(dev) - 1
	for i >= 0 && dev[i] >= '0' && dev[i] <= '9' {
		i--
	}
	if i < len(dev)-1 {
		return dev[:i+1]
	}
	return dev
}
