package pgtuner

// Flavor says which PostgreSQL distribution or service answers the connection.
type Flavor struct {
	// Name is postgresql, aurora, rds, cloudsql, alloydb, azure, supabase, neon,
	// yugabytedb, cockroachdb, greenplum, or edb.
	Name string `json:"name"`
	// Managed is true for a hosted service that restricts server settings.
	Managed bool `json:"managed"`
	// Packaging is the distributor text in server_version, such as "Debian 16.2-1.pgdg120+2".
	Packaging string `json:"packaging,omitempty"`
	// Extensions lists distribution extensions found, such as timescaledb or citus.
	Extensions []string `json:"extensions,omitempty"`
	// Evidence lists the signals behind Name.
	Evidence []string `json:"evidence,omitempty"`
}
