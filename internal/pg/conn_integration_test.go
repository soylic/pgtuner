//go:build integration

package pg

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSessionIsGuarded(t *testing.T) {
	dsn := os.Getenv("PGTUNER_TEST_DSN")
	if dsn == "" {
		t.Skip("set PGTUNER_TEST_DSN")
	}
	ctx := context.Background()
	c, err := Connect(ctx, Options{DSN: dsn, StatementTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)

	show := func(name string) string {
		var v string
		if err := c.QueryRow(ctx, "SHOW "+name).Scan(&v); err != nil {
			t.Fatalf("SHOW %s: %v", name, err)
		}
		return v
	}
	if got := show("application_name"); got != "pgtuner" {
		t.Errorf("application_name = %q, want pgtuner", got)
	}
	if got := show("default_transaction_read_only"); got != "on" {
		t.Errorf("default_transaction_read_only = %q, want on", got)
	}
	if got := show("statement_timeout"); got != "1s" {
		t.Errorf("statement_timeout = %q, want 1s", got)
	}
	if got := show("lock_timeout"); got != "5s" {
		t.Errorf("lock_timeout = %q, want 5s", got)
	}

	// A DDL statement returns no row, so Scan reports "no rows" when the statement
	// succeeds. Only a read-only refusal contains "read-only".
	err = c.QueryRow(ctx, "CREATE TEMP TABLE pgtuner_must_fail (x int)").Scan(new(any))
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("a write must fail with a read-only error, got %v", err)
	}
	start := time.Now()
	err = c.QueryRow(ctx, "SELECT pg_sleep(5)").Scan(new(any))
	if err == nil || !strings.Contains(err.Error(), "statement timeout") || time.Since(start) > 3*time.Second {
		t.Errorf("a slow query must stop at the timeout, got %v after %s", err, time.Since(start))
	}
}

func TestSessionKeepsApplicationNameFromDSN(t *testing.T) {
	dsn := os.Getenv("PGTUNER_TEST_DSN")
	if dsn == "" {
		t.Skip("set PGTUNER_TEST_DSN")
	}
	ctx := context.Background()
	c, err := Connect(ctx, Options{DSN: dsn + "?application_name=mine&"})
	if err != nil {
		// The DSN may already have a query string.
		c, err = Connect(ctx, Options{DSN: dsn + "&application_name=mine"})
		if err != nil {
			t.Skipf("cannot build a DSN with application_name from %q: %v", dsn, err)
		}
	}
	defer c.Close(ctx)
	var v string
	if err := c.QueryRow(ctx, "SHOW application_name").Scan(&v); err != nil || v != "mine" {
		t.Errorf("application_name = %q (%v), want the one from the DSN", v, err)
	}
}
