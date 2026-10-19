package checks

import (
	"context"
	"fmt"
	"strings"

	"github.com/soylic/pgtuner/internal/pg"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// src: https://www.postgresql.org/docs/current/high-availability.html
const docHighAvailability = "https://www.postgresql.org/docs/current/high-availability.html"

// TopologyCheck reports where the server sits in a replication group and which
// failover manager or hosted service controls its settings.
type TopologyCheck struct{ clusterScoped }

func (TopologyCheck) Name() string { return "topology" }

// Run turns the topology and flavor detected before the checks into advice.
// It runs no query. Every advice is Info, because detection is context for the other checks.
func (TopologyCheck) Run(_ context.Context, p *Probe) []pgtuner.Advice {
	var out []pgtuner.Advice
	if a := evalTopology(p.Topology); a != nil {
		out = append(out, *a)
	}
	if a := evalManager(p.Topology.Manager); a != nil {
		out = append(out, *a)
	}
	out = append(out, evalPatroniCluster(p.Topology)...)
	if a := evalPatroniError(p.PatroniError); a != nil {
		out = append(out, *a)
	}
	if a := evalFlavor(p.Flavor); a != nil {
		out = append(out, *a)
	}
	return out
}

// evalTopology describes the role and the neighbours of this node.
func evalTopology(t pgtuner.Topology) *pgtuner.Advice {
	if t.Role == "" || t.Role == pg.TopoUnknown {
		return nil
	}
	var parts []string
	if t.NodeName != "" {
		parts = append(parts, fmt.Sprintf("This node reports itself upstream as %q.", t.NodeName))
	}
	if t.Upstream != nil {
		parts = append(parts, "Replicates from "+describeNode(*t.Upstream)+".")
	}
	if n := len(t.Downstream); n > 0 {
		names := make([]string, 0, n)
		for _, d := range t.Downstream {
			names = append(names, describeNode(d))
		}
		parts = append(parts, fmt.Sprintf("%d node(s) replicate from this server: %s.", n, strings.Join(names, "; ")))
	}
	if t.Upstream == nil && len(t.Downstream) == 0 {
		parts = append(parts, "No node replicates from this server and it replicates from none.")
	}
	if t.SystemID != "" {
		parts = append(parts, fmt.Sprintf("System identifier %s, timeline %d.", t.SystemID, t.Timeline))
	}
	a := pgtuner.Advice{
		ID:      "topology.replication-role",
		Level:   pgtuner.Info,
		Section: "Topology",
		Title:   "Replication topology: " + strings.ReplaceAll(t.Role, "_", " "),
		Message: strings.Join(parts, " "),
		Detail:  "pgtuner sees only the nodes linked to this server. Run it on each node to see the whole group. Nodes of one group share the system identifier.",
		DocRef:  docHighAvailability,
	}
	return &a
}

// describeNode formats one replication neighbour, leaving out empty fields.
func describeNode(n pgtuner.Node) string {
	addr := n.Host
	if n.Port != 0 {
		addr = fmt.Sprintf("%s:%d", n.Host, n.Port)
	}
	s := n.Name
	switch {
	case s == "":
		s = addr
	case addr != "":
		s += " at " + addr
	}
	var meta []string
	if n.State != "" {
		meta = append(meta, n.State)
	}
	if n.SyncState != "" {
		meta = append(meta, n.SyncState)
	}
	if len(meta) > 0 {
		s += " (" + strings.Join(meta, ", ") + ")"
	}
	return s
}

// managerAdvice is the settings guidance for each failover manager.
// src: https://patroni.readthedocs.io/en/latest/dynamic_configuration.html
// src: https://www.repmgr.org/docs/current/
var managerAdvice = map[string]string{
	"patroni":          "Patroni rewrites postgresql.conf and stores shared settings in the DCS. Change settings with patronictl edit-config, not by editing postgresql.conf or running ALTER SYSTEM on one node.",
	"repmgr":           "repmgr does not manage postgresql.conf. Apply each setting change to every node, and restart the standbys before the primary when a setting needs a restart.",
	"pg_auto_failover": "pg_auto_failover keeps its settings in the monitor and in each node's postgresql.conf. Apply each setting change to every node.",
	"citus":            "Citus coordinator and worker nodes keep separate settings. Apply each setting change to every node.",
}

// evalManager reports a detected failover manager and how to change settings under it.
func evalManager(m *pgtuner.Manager) *pgtuner.Advice {
	if m == nil {
		return nil
	}
	a := pgtuner.Advice{
		ID:      "topology.manager-detected",
		Level:   pgtuner.Info,
		Section: "Topology",
		Title:   fmt.Sprintf("Cluster manager detected: %s (%s)", m.Name, m.Confidence),
		Message: "Evidence: " + strings.Join(m.Evidence, "; ") + ".",
		Detail:  managerAdvice[m.Name],
		DocRef:  docHighAvailability,
	}
	if m.Confidence != "certain" {
		a.Message += " This is a guess from names, not proof."
	}
	return &a
}

// evalFlavor reports a distribution other than plain PostgreSQL, or a hosted service.
func evalFlavor(f pgtuner.Flavor) *pgtuner.Advice {
	if f.Name == "" || (f.Name == "postgresql" && !f.Managed && len(f.Extensions) == 0) {
		return nil
	}
	a := pgtuner.Advice{
		ID:      "topology.flavor-detected",
		Level:   pgtuner.Info,
		Section: "Topology",
		Title:   "PostgreSQL distribution: " + f.Name,
		Message: "Evidence: " + strings.Join(f.Evidence, "; ") + ".",
		DocRef:  docHighAvailability,
	}
	if f.Packaging != "" {
		a.Message += " Package: " + f.Packaging + "."
	}
	if f.Managed {
		a.Detail = "This is a hosted service. It may block ALTER SYSTEM and operating system advice. Change settings through the provider's parameter settings."
	} else if f.Name != "postgresql" {
		a.Detail = "This engine is not community PostgreSQL. Some pg_stat views and thresholds may mean something different."
	}
	return &a
}

// evalPatroniCluster reads the member list of the Patroni REST API. It runs only
// when the API answered, so Manager.Confidence is "certain".
// src: https://patroni.readthedocs.io/en/latest/pause.html
func evalPatroniCluster(t pgtuner.Topology) []pgtuner.Advice {
	m := t.Manager
	if m == nil || m.Name != "patroni" || m.Confidence != "certain" || len(t.Members) == 0 {
		return nil
	}
	var out []pgtuner.Advice
	leader := false
	for _, mem := range t.Members {
		if mem.Role == "leader" || mem.Role == "standby_leader" {
			leader = true
		}
		if !mem.Active {
			out = append(out, pgtuner.Advice{
				ID:      "topology.patroni-member-unhealthy",
				Level:   pgtuner.Warn,
				Section: "Topology",
				Title:   fmt.Sprintf("Patroni member %s is %s", mem.Name, mem.State),
				Message: fmt.Sprintf("Patroni lists %s (%s) in state %q. A healthy member is running or streaming.", mem.Name, mem.Role, mem.State),
				Detail:  "Check the Patroni log on that node. Run patronictl list for the state of the whole cluster.",
				DocRef:  docPatroni,
			})
		}
	}
	if !leader {
		out = append(out, pgtuner.Advice{
			ID:      "topology.patroni-no-leader",
			Level:   pgtuner.Bad,
			Section: "Topology",
			Title:   "Patroni reports no leader",
			Message: "No member has the leader role. No node accepts writes until Patroni elects one.",
			Detail:  "Run patronictl list. Check that the DCS (etcd, Consul or ZooKeeper) is reachable from every node.",
			DocRef:  docPatroni,
		})
	}
	if m.Paused {
		out = append(out, pgtuner.Advice{
			ID:      "topology.patroni-paused",
			Level:   pgtuner.Warn,
			Section: "Topology",
			Title:   "Patroni is in pause mode",
			Message: "Patroni does not fail over by itself while it is paused. If the primary fails, nobody promotes a standby.",
			Detail:  "Run patronictl resume when the maintenance is over.",
			DocRef:  "https://patroni.readthedocs.io/en/latest/pause.html",
		})
	}
	return out
}

// evalPatroniError reports that -patroni-url did not give a Patroni answer.
func evalPatroniError(msg string) *pgtuner.Advice {
	if msg == "" {
		return nil
	}
	return &pgtuner.Advice{
		ID:      "topology.patroni-api-failed",
		Level:   pgtuner.Unknown,
		Section: "Topology",
		Title:   "Patroni REST API did not answer",
		Message: "pgtuner could not read the cluster from the URL in -patroni-url: " + msg + ".",
		Detail:  "Check the URL and that port 8008 (the default) is open from this host. Without the API, pgtuner can only guess from names.",
		DocRef:  docPatroni,
	}
}

// src: https://patroni.readthedocs.io/en/latest/rest_api.html
const docPatroni = "https://patroni.readthedocs.io/en/latest/rest_api.html"
