package checks

import (
	"fmt"
	"strings"
)

// Default returns the 23 tuning checks in report order.
// Storage and autovacuum each split into a cluster-wide settings check and a per-database stats check.
// Under -all-databases, instance-level GUC advice then appears once, not once per database.
func Default() Registry {
	return Registry{
		ConnectionCheck{},
		VersionCheck{},
		TopologyCheck{},
		EnvironmentCheck{},
		MemoryCheck{},
		HugePagesCheck{},
		KernelCheck{},
		CacheCheck{},
		WALCheck{},
		CheckpointsCheck{},
		PlannerCheck{},
		StorageSettingsCheck{},
		StorageCheck{},
		IOCheck{},
		AutovacuumSettingsCheck{},
		AutovacuumCheck{},
		SecurityCheck{},
		StatisticsCheck{},
		ReplicationCheck{},
		SchemaCheck{},
		WorkloadCheck{},
		QueryStatsCheck{},
		ConfigCheck{},
	}
}

// ClusterWide returns the Default() checks with ClusterScope, in order.
// Under -all-databases these run once against the connection database.
func ClusterWide() Registry {
	return Default().withScope(ClusterScope)
}

// PerDatabase returns the Default() checks with DatabaseScope, in order.
// Under -all-databases these run once per user database.
func PerDatabase() Registry {
	return Default().withScope(DatabaseScope)
}

func (r Registry) withScope(s Scope) Registry {
	var out Registry
	for _, c := range r {
		if c.Scope() == s {
			out = append(out, c)
		}
	}
	return out
}

// Names returns the check names in report order.
func (r Registry) Names() []string {
	out := make([]string, 0, len(r))
	for _, c := range r {
		out = append(out, c.Name())
	}
	return out
}

// Select keeps the checks named in only (all of them when only is empty) and
// drops the ones named in skip. A name that no check has is an error, so a typo
// does not silently turn a check off.
func (r Registry) Select(only, skip []string) (Registry, error) {
	known := make(map[string]bool, len(r))
	for _, c := range r {
		known[c.Name()] = true
	}
	for _, n := range append(append([]string{}, only...), skip...) {
		if !known[n] {
			return nil, fmt.Errorf("unknown check %q (known: %s)", n, strings.Join(r.Names(), ", "))
		}
	}
	return r.Filter(only, skip), nil
}

// Filter is Select without the name check. Use it on a registry that holds only
// some of the checks, such as ClusterWide, after Select has checked the names
// against the full registry.
func (r Registry) Filter(only, skip []string) Registry {
	has := func(list []string, n string) bool {
		for _, x := range list {
			if x == n {
				return true
			}
		}
		return false
	}
	var out Registry
	for _, c := range r {
		if (len(only) == 0 || has(only, c.Name())) && !has(skip, c.Name()) {
			out = append(out, c)
		}
	}
	return out
}
