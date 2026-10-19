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

// LocalInspector reads OS facts from /proc on the local host. Run it on the database host.
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

// detectCgroupVersion returns 2 if /sys/fs/cgroup/cgroup.controllers exists, 1 if
// /sys/fs/cgroup/memory exists, else 0.
// src: https://docs.kernel.org/admin-guide/cgroup-v2.html
func detectCgroupVersion() int {
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err == nil {
		return 2
	}
	if _, err := os.Stat("/sys/fs/cgroup/memory"); err == nil {
		return 1
	}
	return 0
}

// detectEnvKind uses /proc, /sys, and environment variables. Precedence:
//  1. Kubernetes: KUBERNETES_SERVICE_HOST set or cgroup path contains "kubepods".
//  2. Docker: /.dockerenv present or cgroup path contains "docker".
//  3. LXC: /proc/1/environ contains "lxc" or cgroup path contains "lxc".
//  4. VM: /sys/class/dmi/id/product_name matches a known hypervisor string.
//  5. BareMetal: none of the above matched.
//
// src: https://docs.kernel.org/admin-guide/cgroup-v2.html (cgroup paths)
// src: https://www.postgresql.org/docs/current/kernel-resources.html
func detectEnvKind() EnvKind {
	cgroupContent := readCgroupContent()

	// The kubelet sets KUBERNETES_SERVICE_HOST in every pod, and pod cgroup names start with "kubepods".
	// src: https://kubernetes.io/docs/concepts/services-networking/connect-applications-service/
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return EnvKubernetes
	}
	if strings.Contains(cgroupContent, "kubepods") {
		return EnvKubernetes
	}

	// Docker creates /.dockerenv in every container.
	// src: https://docs.docker.com/engine/reference/run/
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return EnvDocker
	}
	if strings.Contains(cgroupContent, "docker") {
		return EnvDocker
	}

	// LXC sets container=lxc in PID 1's environment and uses "lxc" cgroup paths.
	if strings.Contains(cgroupContent, "/lxc/") || strings.Contains(cgroupContent, "lxc-") {
		return EnvLXC
	}
	if lxcInEnviron() {
		return EnvLXC
	}

	// The DMI product name identifies the hypervisor.
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

// readCgroupContent reads /proc/1/cgroup, which shows container names. It returns "" on failure.
// src: https://docs.kernel.org/admin-guide/cgroup-v2.html
func readCgroupContent() string {
	b, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return ""
	}
	return string(b)
}

// lxcInEnviron reports whether /proc/1/environ contains "container=lxc". It returns false on any error.
func lxcInEnviron() bool {
	b, err := os.ReadFile("/proc/1/environ")
	if err != nil {
		return false
	}
	// environ entries are NUL-delimited.
	for _, entry := range bytes.Split(b, []byte{0}) {
		if string(entry) == "container=lxc" {
			return true
		}
	}
	return false
}

// readFileString returns the trimmed file content, or "" on error.
func readFileString(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Environment returns the detected environment. It never fails.
// src: https://docs.kernel.org/admin-guide/cgroup-v2.html
// src: https://www.postgresql.org/docs/current/kernel-resources.html
func (LocalInspector) Environment() (Environment, error) {
	return Environment{
		Kind:          detectEnvKind(),
		CgroupVersion: detectCgroupVersion(),
	}, nil
}

// EffectiveMemoryBytes returns (limit, true, nil) when the cgroup limit is below
// host RAM, else (hostMemTotal, false, nil).
//
// Cgroup v2 reads /sys/fs/cgroup/memory.max, where "max" means unlimited.
// src: https://docs.kernel.org/admin-guide/cgroup-v2.html#memory-interface-files
//
// Cgroup v1 reads /sys/fs/cgroup/memory/memory.limit_in_bytes, where a value near
// math.MaxInt64 means unlimited.
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
			return hostTotal, false, nil
		}
		limit, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
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
		// The kernel's "unlimited" value is page-aligned just below 2^63 (typically
		// 0x7FFFFFFFFFFFF000). Any limit >= 2^62 counts as unlimited.
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

	return hostTotal, false, nil
}

// EffectiveCPUs returns (quota/period, true, nil) when a cgroup CPU quota is below
// the host CPU count, else (float64(runtime.NumCPU()), false, nil).
//
// Cgroup v2 reads /sys/fs/cgroup/cpu.max as "<quota> <period>", where quota may be "max".
// src: https://docs.kernel.org/admin-guide/cgroup-v2.html#cpu-interface-files
//
// Cgroup v1 reads cpu.cfs_quota_us and cpu.cfs_period_us in /sys/fs/cgroup/cpu/. A quota of -1 means unlimited.
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

// TransparentHugepages returns the bracketed mode from the sysfs file, for example "madvise" from "always [madvise] never".
// src: https://docs.kernel.org/admin-guide/mm/transhuge.html
func (LocalInspector) TransparentHugepages() (string, error) {
	const thpPath = "/sys/kernel/mm/transparent_hugepage/enabled"
	raw := readFileString(thpPath)
	if raw == "" {
		return "", fmt.Errorf("transparent hugepage file unavailable: %s", thpPath)
	}
	start := strings.Index(raw, "[")
	end := strings.Index(raw, "]")
	if start < 0 || end < 0 || end <= start {
		return "", fmt.Errorf("unrecognised THP format: %q", raw)
	}
	return raw[start+1 : end], nil
}

// StorageDevices finds the root mount in /proc/mounts and reads
// /sys/block/<device>/queue/rotational. It returns an empty slice and nil error when the device is unknown.

// src: /proc/mounts format, https://man7.org/linux/man-pages/man5/fstab.5.html
// src: /sys/block rotational, https://www.kernel.org/doc/html/latest/block/queue-sysfs.html
func (LocalInspector) StorageDevices() ([]StorageDevice, error) {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return nil, nil //nolint:nilerr // best-effort
	}
	defer f.Close()

	// The last "/" entry in /proc/mounts is the effective root mount.
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

	// sysfs lists the whole disk, so strip the partition number (/dev/sda1 to sda).
	base := filepath.Base(rootDev)
	rotPath := "/sys/block/" + base + "/queue/rotational"
	if _, err := os.Stat(rotPath); os.IsNotExist(err) {
		base = stripPartitionSuffix(base)
		rotPath = "/sys/block/" + base + "/queue/rotational"
	}
	rotRaw := readFileString(rotPath)
	if rotRaw == "1" {
		sd.Rotational = true
	}

	return []StorageDevice{sd}, nil
}

// stripPartitionSuffix strips a partition number: sda1 to sda, nvme0n1p1 to nvme0n1.
// It handles only the simple cases.
func stripPartitionSuffix(dev string) string {
	// nvme and mmcblk names end in "p<digits>".
	for i := len(dev) - 1; i >= 0; i-- {
		if dev[i] < '0' || dev[i] > '9' {
			if i < len(dev)-1 && dev[i] == 'p' {
				return dev[:i]
			}
			break
		}
	}
	// sd and vd names end in digits.
	i := len(dev) - 1
	for i >= 0 && dev[i] >= '0' && dev[i] <= '9' {
		i--
	}
	if i < len(dev)-1 {
		return dev[:i+1]
	}
	return dev
}
