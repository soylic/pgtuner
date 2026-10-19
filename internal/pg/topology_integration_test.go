//go:build integration

package pg

import (
	"context"
	"os"
	"testing"
)

func TestDetectTopologyAndFlavorLive(t *testing.T) {
	dsn := os.Getenv("PGTUNER_TEST_DSN")
	if dsn == "" {
		t.Skip("set PGTUNER_TEST_DSN")
	}

	ctx := context.Background()
	c, err := Connect(ctx, Options{DSN: dsn})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close(ctx)

	s, err := LoadSettings(ctx, c)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	role, err := DetectRole(ctx, c)
	if err != nil {
		t.Fatalf("DetectRole: %v", err)
	}

	topo := DetectTopology(ctx, c, role, s)
	if topo.Role != TopoStandalone {
		t.Errorf("Role = %q, want %q on a lone server", topo.Role, TopoStandalone)
	}
	if topo.Upstream != nil || len(topo.Downstream) != 0 {
		t.Errorf("lone server must have no neighbours: %+v", topo)
	}
	if topo.Manager != nil {
		t.Errorf("no failover manager expected: %+v", topo.Manager)
	}

	flavor := DetectFlavor(ctx, c, s)
	if flavor.Name != "postgresql" || flavor.Managed {
		t.Errorf("Flavor = %s managed=%v, want plain postgresql", flavor.Name, flavor.Managed)
	}
}
