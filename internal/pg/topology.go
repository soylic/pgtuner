package pg

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// Topology roles reported in pgtuner.Topology.Role.
const (
	TopoPrimary          = "primary"
	TopoStandby          = "standby"
	TopoCascadingStandby = "cascading_standby"
	TopoStandalone       = "standalone"
	TopoUnknown          = "unknown"
)

// DetectTopology maps the connected server onto a replication group. Each probe
// is independent and failures leave the matching field empty, so the result is
// always usable. It never returns an error.
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-VIEW
// src: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-WAL-RECEIVER-VIEW
func DetectTopology(ctx context.Context, c *Conn, role Role, s *Settings) pgtuner.Topology {
	t := pgtuner.Topology{Role: TopoUnknown}
	if v, ok := s.Get("cluster_name"); ok {
		t.ClusterName = v
	}

	// pg_control_* needs a grant outside superuser, so an error is expected for many roles.
	var sysid string
	if err := c.QueryRow(ctx, "SELECT system_identifier::text FROM pg_control_system()").Scan(&sysid); err == nil {
		t.SystemID = sysid
	}
	var tli int
	if err := c.QueryRow(ctx, "SELECT timeline_id FROM pg_control_checkpoint()").Scan(&tli); err == nil {
		t.Timeline = tli
	}

	t.Downstream = queryDownstream(ctx, c)
	if role.InRecovery {
		t.Upstream, t.NodeName = queryUpstream(ctx, c, s)
	}

	switch role.Role {
	case RoleStandby:
		t.Role = TopoStandby
		if len(t.Downstream) > 0 {
			t.Role = TopoCascadingStandby
		}
	case RolePrimary:
		t.Role = TopoPrimary
	case RoleStandalone:
		t.Role = TopoStandalone
	}

	t.Manager, t.Members = detectManager(ctx, c, s, t)
	return t
}

func queryDownstream(ctx context.Context, c *Conn) []pgtuner.Node {
	const sql = `SELECT coalesce(application_name, ''), coalesce(host(client_addr), ''),
		coalesce(state, ''), coalesce(sync_state, '')
		FROM pg_stat_replication ORDER BY application_name, client_addr`
	rows, err := c.Query(ctx, sql)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []pgtuner.Node
	for rows.Next() {
		var n pgtuner.Node
		if err := rows.Scan(&n.Name, &n.Host, &n.State, &n.SyncState); err != nil {
			return out
		}
		out = append(out, n)
	}
	return out
}

// queryUpstream reads the active WAL receiver and falls back to primary_conninfo.
// It also returns the application_name this standby sends upstream, which names this node.
// The conninfo string can hold a password, so only host, port and application_name leave this function.
func queryUpstream(ctx context.Context, c *Conn, s *Settings) (*pgtuner.Node, string) {
	n := pgtuner.Node{}
	self := ""
	found := false
	const sql = `SELECT coalesce(status, ''), coalesce(sender_host, ''), coalesce(sender_port, 0),
		coalesce(slot_name, '') FROM pg_stat_wal_receiver LIMIT 1`
	if err := c.QueryRow(ctx, sql).Scan(&n.State, &n.Host, &n.Port, &n.Slot); err == nil {
		found = true
	}
	if v, ok := s.Get("primary_conninfo"); ok && v != "" {
		info := ParseConninfo(v)
		if n.Host == "" {
			n.Host = info["host"]
		}
		if n.Port == 0 {
			n.Port, _ = strconv.Atoi(info["port"])
		}
		self = info["application_name"]
		if n.Slot == "" {
			if slot, ok := s.Get("primary_slot_name"); ok {
				n.Slot = slot
			}
		}
		found = found || n.Host != ""
	}
	if !found {
		return nil, self
	}
	return &n, self
}

// ParseConninfo splits a libpq key=value string into a map. It understands
// single-quoted values and backslash escapes. A string that does not parse
// returns the keys read so far.
// src: https://www.postgresql.org/docs/current/libpq-connect.html#LIBPQ-CONNSTRING-KEYWORD-VALUE
func ParseConninfo(s string) map[string]string {
	out := map[string]string{}
	i := 0
	for i < len(s) {
		for i < len(s) && s[i] == ' ' {
			i++
		}
		start := i
		for i < len(s) && s[i] != '=' && s[i] != ' ' {
			i++
		}
		key := s[start:i]
		if i >= len(s) || s[i] != '=' {
			return out
		}
		i++
		var val strings.Builder
		if i < len(s) && s[i] == '\'' {
			i++
			for i < len(s) && s[i] != '\'' {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				val.WriteByte(s[i])
				i++
			}
			i++
		} else {
			for i < len(s) && s[i] != ' ' {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				val.WriteByte(s[i])
				i++
			}
		}
		out[key] = val.String()
	}
	return out
}

// detectManager looks for a failover manager or cluster extension. Catalog hits
// are "certain" and also give the member list. Name patterns are "probable" or "possible".
func detectManager(ctx context.Context, c *Conn, s *Settings, t pgtuner.Topology) (*pgtuner.Manager, []pgtuner.Member) {
	if m, members := detectRepmgr(ctx, c); m != nil {
		return m, members
	}
	if m, members := detectPgAutoFailover(ctx, c); m != nil {
		return m, members
	}
	if m, members := detectCitus(ctx, c); m != nil {
		return m, members
	}
	return detectPatroni(ctx, c, s, t), nil
}

func tableExists(ctx context.Context, c *Conn, name string) bool {
	var ok bool
	err := c.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", name).Scan(&ok)
	return err == nil && ok
}

// src: https://www.repmgr.org/docs/current/repmgr-cluster-show.html
func detectRepmgr(ctx context.Context, c *Conn) (*pgtuner.Manager, []pgtuner.Member) {
	if !tableExists(ctx, c, "repmgr.nodes") {
		return nil, nil
	}
	m := &pgtuner.Manager{Name: "repmgr", Confidence: "certain", Evidence: []string{"table repmgr.nodes exists"}}
	rows, err := c.Query(ctx, `SELECT node_name, coalesce(conninfo, ''), type, active FROM repmgr.nodes ORDER BY node_id`)
	if err != nil {
		return m, nil
	}
	defer rows.Close()
	var members []pgtuner.Member
	for rows.Next() {
		var mem pgtuner.Member
		var conninfo string
		if err := rows.Scan(&mem.Name, &conninfo, &mem.Role, &mem.Active); err != nil {
			break
		}
		info := ParseConninfo(conninfo)
		mem.Host = info["host"]
		mem.Port, _ = strconv.Atoi(info["port"])
		members = append(members, mem)
	}
	return m, members
}

// src: https://pg-auto-failover.readthedocs.io/en/main/ref/pg_autoctl_show_state.html
func detectPgAutoFailover(ctx context.Context, c *Conn) (*pgtuner.Manager, []pgtuner.Member) {
	if !tableExists(ctx, c, "pgautofailover.node") {
		return nil, nil
	}
	m := &pgtuner.Manager{Name: "pg_auto_failover", Confidence: "certain",
		Evidence: []string{"table pgautofailover.node exists (this is the monitor database)"}}
	rows, err := c.Query(ctx, `SELECT nodename, nodehost, nodeport, reportedstate::text FROM pgautofailover.node ORDER BY nodeid`)
	if err != nil {
		return m, nil
	}
	defer rows.Close()
	var members []pgtuner.Member
	for rows.Next() {
		var mem pgtuner.Member
		if err := rows.Scan(&mem.Name, &mem.Host, &mem.Port, &mem.Role); err != nil {
			break
		}
		mem.Active = true
		members = append(members, mem)
	}
	return m, members
}

// src: https://docs.citusdata.com/en/stable/develop/api_metadata.html#worker-node-table
func detectCitus(ctx context.Context, c *Conn) (*pgtuner.Manager, []pgtuner.Member) {
	if !tableExists(ctx, c, "pg_catalog.pg_dist_node") {
		return nil, nil
	}
	m := &pgtuner.Manager{Name: "citus", Confidence: "certain", Evidence: []string{"table pg_dist_node exists"}}
	rows, err := c.Query(ctx, `SELECT nodename, nodeport, noderole::text, isactive FROM pg_catalog.pg_dist_node ORDER BY nodeid`)
	if err != nil {
		return m, nil
	}
	defer rows.Close()
	var members []pgtuner.Member
	for rows.Next() {
		var mem pgtuner.Member
		if err := rows.Scan(&mem.Host, &mem.Port, &mem.Role, &mem.Active); err != nil {
			break
		}
		mem.Name = fmt.Sprintf("%s:%d", mem.Host, mem.Port)
		members = append(members, mem)
	}
	return m, members
}

// detectPatroni has no catalog to read: Patroni keeps its state in a DCS such as etcd.
// It reports evidence from names only, so the confidence stays below "certain".
// src: https://patroni.readthedocs.io/en/latest/replication_modes.html
func detectPatroni(ctx context.Context, c *Conn, s *Settings, t pgtuner.Topology) *pgtuner.Manager {
	var evidence []string
	confidence := "possible"

	var n int
	if err := c.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE application_name LIKE 'Patroni%'").Scan(&n); err == nil && n > 0 {
		evidence = append(evidence, fmt.Sprintf("%d session(s) with application_name starting with 'Patroni'", n))
		confidence = "probable"
	}

	// Patroni names each replication slot and each standby application_name after the member.
	slots := physicalSlotNames(ctx, c)
	if len(slots) > 0 {
		var match []string
		for _, d := range t.Downstream {
			if d.Name != "" && slots[d.Name] {
				match = append(match, d.Name)
			}
		}
		if t.Upstream != nil && t.NodeName != "" && t.Upstream.Slot == t.NodeName {
			match = append(match, t.NodeName)
		}
		if len(match) > 0 && t.ClusterName != "" {
			evidence = append(evidence, fmt.Sprintf("cluster_name=%q and a replication slot is named like its standby (%s)", t.ClusterName, strings.Join(match, ", ")))
		}
	}
	if len(evidence) == 0 {
		return nil
	}
	return &pgtuner.Manager{Name: "patroni", Confidence: confidence, Evidence: evidence}
}

func physicalSlotNames(ctx context.Context, c *Conn) map[string]bool {
	rows, err := c.Query(ctx, "SELECT slot_name FROM pg_replication_slots WHERE slot_type = 'physical'")
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			break
		}
		out[name] = true
	}
	return out
}
