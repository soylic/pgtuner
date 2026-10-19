package pgtuner

// Topology describes where the analyzed server sits in a replication group,
// as seen from that server alone. Every field is best-effort: a field is empty
// when the connected role lacks the privilege to read it.
type Topology struct {
	// Role is one of primary, standby, cascading_standby, standalone, unknown.
	Role string `json:"role"`
	// SystemID is pg_control_system().system_identifier. Nodes of one cluster share it.
	SystemID string `json:"system_identifier,omitempty"`
	// Timeline is the current timeline. It increases on each promotion. A standby reports the one its WAL receiver follows.
	Timeline int `json:"timeline,omitempty"`
	// ClusterName is the cluster_name setting. Failover managers often set it.
	ClusterName string `json:"cluster_name,omitempty"`
	// NodeName is the application_name this standby sends upstream. Empty on a primary.
	NodeName string `json:"node_name,omitempty"`
	// Upstream is the node this server replicates from. Set on standbys only.
	Upstream *Node `json:"upstream,omitempty"`
	// Downstream lists the nodes that replicate from this server.
	Downstream []Node `json:"downstream,omitempty"`
	// Members lists nodes that a failover manager knows about, when its catalog is readable.
	Members []Member `json:"members,omitempty"`
	// Manager is the failover manager or cluster extension detected, if any.
	Manager *Manager `json:"manager,omitempty"`
}

// Node is one end of a replication connection.
type Node struct {
	Name      string `json:"name,omitempty"`       // application_name
	Host      string `json:"host,omitempty"`       // client address, or sender host for an upstream
	Port      int    `json:"port,omitempty"`       // upstream only
	State     string `json:"state,omitempty"`      // streaming, catchup, and so on
	SyncState string `json:"sync_state,omitempty"` // async, potential, sync, quorum
	Slot      string `json:"slot,omitempty"`
}

// Member is one node listed in a failover manager's own catalog.
type Member struct {
	Name   string `json:"name"`
	Host   string `json:"host,omitempty"`
	Port   int    `json:"port,omitempty"`
	Role   string `json:"role,omitempty"`
	Active bool   `json:"active"`
	// State, Timeline and LagBytes come from the Patroni REST API only. LagBytes is nil when unknown.
	State    string `json:"state,omitempty"`
	Timeline int    `json:"timeline,omitempty"`
	LagBytes *int64 `json:"lag_bytes,omitempty"`
}

// Manager names a detected failover manager or cluster extension.
type Manager struct {
	Name string `json:"name"` // patroni, repmgr, pg_auto_failover, citus
	// Confidence is "certain" when a catalog proves it, else "probable" or "possible".
	Confidence string   `json:"confidence"`
	Evidence   []string `json:"evidence"`
	// Paused is true when Patroni runs in pause mode, so it does not fail over by itself.
	Paused bool `json:"paused,omitempty"`
}
