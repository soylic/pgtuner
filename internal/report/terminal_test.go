package report

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

func makeTestReport() *pgtuner.Report {
	advice := []pgtuner.Advice{
		{
			Level:   pgtuner.Warn,
			Section: "memory",
			Title:   "shared_buffers low",
			Message: "shared_buffers is below 25% of RAM.",
			Detail:  "Current value is 128MB.",
			DocRef:  "https://www.postgresql.org/docs/current/runtime-config-resource.html",
		},
		{
			Level:   pgtuner.OK,
			Section: "security",
			Title:   "ssl enabled",
			Message: "SSL is enabled.",
		},
		{
			Level:   pgtuner.Info,
			Section: "memory",
			Title:   "work_mem default",
			Message: "work_mem is at default.",
		},
	}
	return pgtuner.NewReport("16.2", advice)
}

func TestWriteTerminalPlain(t *testing.T) {
	rep := makeTestReport()
	var buf bytes.Buffer
	if err := WriteTerminal(&buf, rep, false, false); err != nil {
		t.Fatalf("WriteTerminal: %v", err)
	}

	const want = `PostgreSQL Tuning Report — server 16.2

=== memory ===
  [warn]  shared_buffers low — shared_buffers is below 25% of RAM.
          Detail: Current value is 128MB.
          Ref:    https://www.postgresql.org/docs/current/runtime-config-resource.html
  [info]  work_mem default — work_mem is at default.

=== security ===
  [ok]    ssl enabled — SSL is enabled.

Summary:  1 ok  1 info  0 todo  1 warn  0 bad  0 unknown   Grade: A
`
	got := buf.String()
	if got != want {
		t.Errorf("terminal output mismatch.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteTerminalSeveritySort(t *testing.T) {
	advice := []pgtuner.Advice{
		{Level: pgtuner.Bad, Section: "wal", Title: "fsync off", Message: "dangerous"},
		{Level: pgtuner.Info, Section: "memory", Title: "work_mem", Message: "default"},
	}
	rep := pgtuner.NewReport("16.2", advice)
	var buf bytes.Buffer
	if err := WriteTerminal(&buf, rep, false, true); err != nil {
		t.Fatalf("WriteTerminal: %v", err)
	}
	got := buf.Bytes()
	if !bytes.Contains(got, []byte("=== Findings (by severity) ===")) {
		t.Errorf("expected severity-sorted header, got:\n%s", got)
	}
	if !bytes.Contains(got, []byte("(wal)")) || !bytes.Contains(got, []byte("(memory)")) {
		t.Errorf("expected section labels for differing sections, got:\n%s", got)
	}
}

// TestWriteTerminalSeveritySortDedupesSection verifies the "(section)" label is
// printed only when the section changes between consecutive items.
func TestWriteTerminalSeveritySortDedupesSection(t *testing.T) {
	advice := []pgtuner.Advice{
		{Level: pgtuner.Bad, Section: "wal", Title: "fsync off", Message: "dangerous"},
		{Level: pgtuner.Warn, Section: "wal", Title: "wal_level", Message: "minimal"},
		{Level: pgtuner.Info, Section: "memory", Title: "work_mem", Message: "default"},
	}
	rep := pgtuner.NewReport("16.2", advice)
	var buf bytes.Buffer
	if err := WriteTerminal(&buf, rep, false, true); err != nil {
		t.Fatalf("WriteTerminal: %v", err)
	}
	got := buf.String()
	// Two consecutive wal items, but the "(wal)" label must appear exactly once.
	if n := strings.Count(got, "(wal)"); n != 1 {
		t.Errorf("expected '(wal)' label once for consecutive same-section items, got %d:\n%s", n, got)
	}
	if n := strings.Count(got, "(memory)"); n != 1 {
		t.Errorf("expected '(memory)' label once, got %d:\n%s", n, got)
	}
}

func TestColorEnabledNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	// NO_COLOR must take precedence over everything else.
	// os.Stdout is not a terminal in test/CI environments, but we are
	// specifically exercising the NO_COLOR early-return path.
	if ColorEnabled(os.Stdout) {
		t.Error("ColorEnabled: expected false when NO_COLOR is set")
	}
}

func TestColorEnabledNonTerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	// Ensure NO_COLOR is unset so the terminal check is what decides.
	t.Setenv("NO_COLOR", "")
	os.Unsetenv("NO_COLOR")
	if ColorEnabled(w) {
		t.Fatal("a pipe is not a terminal; color should be disabled")
	}
}
