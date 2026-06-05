//go:build integration

package pg

import (
	"context"
	"os"
	"testing"
)

func TestLoadSettingsLive(t *testing.T) {
	dsn := os.Getenv("PGTUNER_TEST_DSN")
	if dsn == "" {
		t.Skip("set PGTUNER_TEST_DSN")
	}
	c, err := Connect(context.Background(), Options{DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())

	s, err := LoadSettings(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.raw) == 0 {
		t.Fatal("settings snapshot is empty")
	}
	if _, ok := s.Get("max_connections"); !ok {
		t.Fatal("max_connections not present in settings snapshot")
	}
}
