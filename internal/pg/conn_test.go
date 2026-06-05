//go:build integration

package pg

import (
	"context"
	"os"
	"testing"
)

func TestConnectLive(t *testing.T) {
	dsn := os.Getenv("PGTUNER_TEST_DSN")
	if dsn == "" {
		t.Skip("set PGTUNER_TEST_DSN")
	}
	c, err := Connect(context.Background(), Options{DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	if !c.Version.AtLeast(15, 0) {
		t.Fatalf("unexpected version %s", c.Version)
	}
}
