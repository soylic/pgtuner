//go:build integration

package pg

import (
	"context"
	"os"
	"testing"
)

func TestDetectRoleLive(t *testing.T) {
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

	r, err := DetectRole(ctx, c)
	if err != nil {
		t.Fatalf("DetectRole returned error: %v", err)
	}
	// The dev PG16 is a standalone server: not in recovery, 0 standbys.
	// src: https://www.postgresql.org/docs/current/functions-admin.html (pg_is_in_recovery)
	// src: https://www.postgresql.org/docs/current/monitoring-stats.html (pg_stat_replication)
	if r.Role != RoleStandalone {
		t.Errorf("expected RoleStandalone, got %q (InRecovery=%v, ConnectedStandbys=%d)",
			r.Role, r.InRecovery, r.ConnectedStandbys)
	}
	if r.InRecovery {
		t.Errorf("expected InRecovery=false on standalone server")
	}
	if r.ConnectedStandbys != 0 {
		t.Errorf("expected ConnectedStandbys=0 on standalone server, got %d", r.ConnectedStandbys)
	}
}
