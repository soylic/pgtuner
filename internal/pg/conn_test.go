//go:build integration

package pg

import (
	"context"
	"os"
	"regexp"
	"testing"
)

// liveVersionRE matches a major.minor version string. The CI matrix runs
// against PG 14-18, so we assert a sane shape rather than a single major.
var liveVersionRE = regexp.MustCompile(`^\d+\.\d+$`)

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
	// 14 is the oldest version pgtuner supports (and the CI matrix floor).
	if !c.Version.AtLeast(14, 0) {
		t.Fatalf("unexpected version %s", c.Version)
	}
	if !liveVersionRE.MatchString(c.Version.String()) {
		t.Fatalf("version string %q does not match major.minor shape", c.Version)
	}
}
