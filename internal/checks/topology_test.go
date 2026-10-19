package checks

import (
	"strings"
	"testing"

	"github.com/soylic/pgtuner/pkg/pgtuner"
)

func TestEvalTopologyUnknownIsSilent(t *testing.T) {
	if a := evalTopology(pgtuner.Topology{Role: "unknown"}); a != nil {
		t.Errorf("want nil for unknown role, got %+v", a)
	}
}

func TestEvalTopologyCascadingStandby(t *testing.T) {
	a := evalTopology(pgtuner.Topology{
		Role:       "cascading_standby",
		SystemID:   "7500000000000000001",
		Timeline:   3,
		Upstream:   &pgtuner.Node{Name: "pg1", Host: "10.0.0.1", Port: 5432, State: "streaming"},
		Downstream: []pgtuner.Node{{Name: "pg3", Host: "10.0.0.3", State: "streaming", SyncState: "async"}},
	})
	if a == nil {
		t.Fatal("want advice")
	}
	if a.Level != pgtuner.Info || a.Section != "Topology" {
		t.Errorf("level/section = %v/%s", a.Level, a.Section)
	}
	for _, want := range []string{"cascading standby", "pg1 at 10.0.0.1:5432", "pg3 at 10.0.0.3", "timeline 3"} {
		if !strings.Contains(a.Title+" "+a.Message, want) {
			t.Errorf("advice missing %q: %s | %s", want, a.Title, a.Message)
		}
	}
}

func TestEvalManager(t *testing.T) {
	if evalManager(nil) != nil {
		t.Error("nil manager must give no advice")
	}
	a := evalManager(&pgtuner.Manager{Name: "patroni", Confidence: "probable", Evidence: []string{"1 session(s) with application_name starting with 'Patroni'"}})
	if a == nil || !strings.Contains(a.Detail, "patronictl edit-config") {
		t.Fatalf("patroni advice must name patronictl edit-config: %+v", a)
	}
	if !strings.Contains(a.Message, "not proof") {
		t.Errorf("a non-certain match must say it is a guess: %s", a.Message)
	}
	c := evalManager(&pgtuner.Manager{Name: "repmgr", Confidence: "certain", Evidence: []string{"table repmgr.nodes exists"}})
	if strings.Contains(c.Message, "not proof") {
		t.Errorf("a certain match must not say it is a guess: %s", c.Message)
	}
}

func TestEvalFlavor(t *testing.T) {
	if evalFlavor(pgtuner.Flavor{Name: "postgresql"}) != nil {
		t.Error("plain postgresql must give no advice")
	}
	a := evalFlavor(pgtuner.Flavor{Name: "rds", Managed: true, Evidence: []string{"settings with prefix rds."}, Packaging: "Debian 16.2"})
	if a == nil || !strings.Contains(a.Detail, "hosted service") || !strings.Contains(a.Message, "Debian 16.2") {
		t.Errorf("managed advice incomplete: %+v", a)
	}
	e := evalFlavor(pgtuner.Flavor{Name: "postgresql", Extensions: []string{"timescaledb"}, Evidence: []string{"extensions: timescaledb"}})
	if e == nil || e.Detail != "" {
		t.Errorf("extension-only flavor needs advice with no detail: %+v", e)
	}
}
