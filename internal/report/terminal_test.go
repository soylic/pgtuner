package report

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/soylic/pgtuner/pkg/pgtuner"
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

	const want = `PostgreSQL Tuning Report, server 16.2

=== memory ===
  [warn]  shared_buffers low: shared_buffers is below 25% of RAM.
          Detail: Current value is 128MB.
          Ref:    https://www.postgresql.org/docs/current/runtime-config-resource.html
  [info]  work_mem default: work_mem is at default.

=== security ===
  [ok]    ssl enabled: SSL is enabled.

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
	if !bytes.Contains(got, []byte("=== Bad (1) ===")) || !bytes.Contains(got, []byte("=== Info (1) ===")) {
		t.Errorf("expected one heading per level, got:\n%s", got)
	}
	if !bytes.Contains(got, []byte("(wal)")) || !bytes.Contains(got, []byte("(memory)")) {
		t.Errorf("expected section labels for differing sections, got:\n%s", got)
	}
}

func TestWriteTerminalSeveritySortDedupesSection(t *testing.T) {
	advice := []pgtuner.Advice{
		{Level: pgtuner.Bad, Section: "wal", Title: "fsync off", Message: "dangerous"},
		{Level: pgtuner.Bad, Section: "wal", Title: "full_page_writes off", Message: "dangerous"},
		{Level: pgtuner.Info, Section: "memory", Title: "work_mem", Message: "default"},
	}
	rep := pgtuner.NewReport("16.2", advice)
	var buf bytes.Buffer
	if err := WriteTerminal(&buf, rep, false, true); err != nil {
		t.Fatalf("WriteTerminal: %v", err)
	}
	got := buf.String()
	// Two wal items at the same level, but the "(wal)" label must appear exactly once.
	if n := strings.Count(got, "(wal)"); n != 1 {
		t.Errorf("expected '(wal)' label once for consecutive same-section items, got %d:\n%s", n, got)
	}
	if n := strings.Count(got, "(memory)"); n != 1 {
		t.Errorf("expected '(memory)' label once, got %d:\n%s", n, got)
	}
}

func TestColorEnabledNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	// NO_COLOR takes precedence. This exercises its early return.
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
	t.Setenv("NO_COLOR", "")
	os.Unsetenv("NO_COLOR")
	if ColorEnabled(w) {
		t.Fatal("a pipe is not a terminal; color should be disabled")
	}
}

func TestWriteTerminalPlainHasNoANSI(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteTerminalOptions(&buf, makeTestReport(), TerminalOptions{Version: "0.1.0"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "\033[") {
		t.Errorf("plain output must not contain ANSI codes:\n%q", buf.String())
	}
	if strings.Contains(buf.String(), "╭") {
		t.Errorf("plain output must not contain the banner:\n%s", buf.String())
	}
}

func TestWriteTerminalColorBanner(t *testing.T) {
	rep := makeTestReport()
	rep.Topology = &pgtuner.Topology{Role: "cascading_standby"}
	var buf bytes.Buffer
	if err := WriteTerminalOptions(&buf, rep, TerminalOptions{Color: true, Version: "0.1.0"}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{"pgtuner", "0.1.0", "PostgreSQL 16.2", "cascading standby", "Grade ", "╭", "╯"} {
		if !strings.Contains(got, want) {
			t.Errorf("banner missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "\033[1m\033[33m[warn]") {
		t.Errorf("warn tag must be bold yellow:\n%q", got)
	}
}

func TestBannerBoxIsRectangular(t *testing.T) {
	var buf bytes.Buffer
	if err := writeBanner(&buf, makeTestReport(), "0.1.0"); err != nil {
		t.Fatal(err)
	}
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	lines := strings.Split(strings.TrimRight(ansi.ReplaceAllString(buf.String(), ""), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 box lines, got %d:\n%s", len(lines), buf.String())
	}
	w := utf8.RuneCountInString(lines[0])
	for _, l := range lines {
		if n := utf8.RuneCountInString(l); n != w {
			t.Errorf("box line width %d, want %d: %q", n, w, l)
		}
	}
}

func TestGradeStyle(t *testing.T) {
	cases := map[string]string{"A": ansiGreen, "B": ansiGreen, "C": ansiYellow, "D": ansiYellow, "F": ansiRed}
	for grade, want := range cases {
		if got := gradeStyle(grade)[1]; got != want {
			t.Errorf("gradeStyle(%q) = %q, want %q", grade, got, want)
		}
	}
}

func TestWriteTerminalSeverityGroupsAndIgnored(t *testing.T) {
	advice := []pgtuner.Advice{
		{Level: pgtuner.Bad, Section: "wal", Title: "fsync off", Message: "x"},
		{Level: pgtuner.Warn, Section: "wal", Title: "wal_level", Message: "y"},
		{Level: pgtuner.Warn, Section: "memory", Title: "work_mem", Message: "z"},
		{Level: pgtuner.Info, Section: "memory", Title: "huge pages", Message: "w"},
	}
	rep := pgtuner.NewReport("16.2", advice)
	rep.Ignored = 3
	var buf bytes.Buffer
	if err := WriteTerminal(&buf, rep, false, true); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	order := []string{"=== Bad (1) ===", "=== Warn (2) ===", "=== Info (1) ==="}
	last := -1
	for _, h := range order {
		i := strings.Index(got, h)
		if i < 0 || i < last {
			t.Fatalf("heading %q missing or out of order:\n%s", h, got)
		}
		last = i
	}
	if strings.Count(got, "(memory)") != 2 {
		t.Errorf("a section label repeats under each level it appears in:\n%s", got)
	}
	if !strings.Contains(got, "(3 ignored)") {
		t.Errorf("footer must say how many findings were ignored:\n%s", got)
	}
}

func TestWriteDiff(t *testing.T) {
	warn, info := pgtuner.Warn, pgtuner.Info
	d := pgtuner.Diff{
		OldGrade: "D", NewGrade: "B",
		Added:    []pgtuner.Change{{ID: "io.sync", Title: "io", New: &info}},
		Worse:    []pgtuner.Change{{ID: "memory.sb", Title: "sb", Old: &info, New: &warn}},
		Resolved: []pgtuner.Change{{ID: "wal.fsync-off", Title: "fsync off", Old: &warn}},
	}
	var buf bytes.Buffer
	if err := WriteDiff(&buf, d, false); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{"Grade: D -> B", "New (1)", "Worse (1)", "Resolved (1)", "[info] -> [warn]  memory.sb"} {
		if !strings.Contains(got, want) {
			t.Errorf("diff output missing %q:\n%s", want, got)
		}
	}
	var none bytes.Buffer
	_ = WriteDiff(&none, pgtuner.Diff{OldGrade: "A", NewGrade: "A"}, false)
	if !strings.Contains(none.String(), "No findings changed.") {
		t.Errorf("empty diff: %q", none.String())
	}
}

func TestWriteTerminalShowIDs(t *testing.T) {
	rep := pgtuner.NewReport("16.2", []pgtuner.Advice{
		{ID: "memory.shared-buffers-low", Level: pgtuner.Warn, Section: "memory", Title: "shared_buffers low", Message: "m", Detail: "d"},
	})
	var with, without bytes.Buffer
	if err := WriteTerminalOptions(&with, rep, TerminalOptions{ShowIDs: true}); err != nil {
		t.Fatal(err)
	}
	if err := WriteTerminalOptions(&without, rep, TerminalOptions{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(with.String(), "ID:      memory.shared-buffers-low") {
		t.Errorf("ID line missing:\n%s", with.String())
	}
	if strings.Contains(without.String(), "memory.shared-buffers-low") {
		t.Errorf("the ID must stay hidden by default:\n%s", without.String())
	}
}
