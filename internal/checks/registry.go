package checks

// Default returns the standard ordered set of 22 tuning checks.
// The order mirrors a logical report flow: connectivity → version safety →
// environment/resource awareness → memory → OS huge pages → OS kernel params →
// buffer cache → WAL safety → checkpoint tuning → query planner →
// storage settings → storage temp-file stats → I/O pressure →
// autovacuum settings → autovacuum per-database stats → security →
// statistics collection → replication health → schema / data-level analysis →
// workload & connection analysis → query performance (pg_stat_statements) →
// configuration depth.
//
// Storage and autovacuum are each split into a cluster-wide settings check and a
// per-database stats check so that, under -all-databases, instance-level GUC
// advice is reported once rather than repeated per database.
func Default() Registry {
	return Registry{
		ConnectionCheck{},
		VersionCheck{},
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

// ClusterWide returns the subset of Default() whose checks examine cluster-wide
// / instance-level state (Scope() == ClusterScope), preserving Default() order.
// Under -all-databases these run once against the connection database.
func ClusterWide() Registry {
	return Default().withScope(ClusterScope)
}

// PerDatabase returns the subset of Default() whose checks examine
// database-scoped state (Scope() == DatabaseScope), preserving Default() order.
// Under -all-databases these run once per user database.
func PerDatabase() Registry {
	return Default().withScope(DatabaseScope)
}

// withScope filters the registry to checks of the given scope, preserving order.
func (r Registry) withScope(s Scope) Registry {
	var out Registry
	for _, c := range r {
		if c.Scope() == s {
			out = append(out, c)
		}
	}
	return out
}
