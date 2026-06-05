package tuner

import (
	"errors"
	"strings"
	"testing"

	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// TestDBFailureAdvice verifies the tagged-Unknown advice produced when a
// per-database analysis fails. This is the branch taken under -all-databases
// when a database cannot be connected to or its settings cannot be loaded.
func TestDBFailureAdvice(t *testing.T) {
	err := errors.New("connect: dial tcp: connection refused")
	a := dbFailureAdvice("payments", err)

	if a.Level != pgtuner.Unknown {
		t.Errorf("Level: got %v, want Unknown", a.Level)
	}
	if a.Database != "payments" {
		t.Errorf("Database: got %q, want %q", a.Database, "payments")
	}
	if a.Section != "Per-Database" {
		t.Errorf("Section: got %q, want %q", a.Section, "Per-Database")
	}
	// The message must attribute the failure to the named database and carry the
	// underlying error so the operator can act on it.
	if !strings.Contains(a.Message, "payments") {
		t.Errorf("Message missing database name: %q", a.Message)
	}
	if !strings.Contains(a.Message, "connection refused") {
		t.Errorf("Message missing underlying error: %q", a.Message)
	}
}
