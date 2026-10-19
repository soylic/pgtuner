package tuner

import (
	"errors"
	"strings"
	"testing"

	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// TestDBFailureAdvice checks the Unknown advice for a failed per-database analysis.
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
	// The message names the database and carries the underlying error.
	if !strings.Contains(a.Message, "payments") {
		t.Errorf("Message missing database name: %q", a.Message)
	}
	if !strings.Contains(a.Message, "connection refused") {
		t.Errorf("Message missing underlying error: %q", a.Message)
	}
}
