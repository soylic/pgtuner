package checks

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
