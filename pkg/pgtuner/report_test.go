package pgtuner

import "testing"

func TestNewReportSummaryCounts(t *testing.T) {
	advice := []Advice{
		{Level: OK, Section: "conn", Title: "a", Message: "m"},
		{Level: OK, Section: "conn", Title: "b", Message: "m"},
		{Level: Warn, Section: "mem", Title: "c", Message: "m"},
		{Level: Bad, Section: "mem", Title: "d", Message: "m"},
		{Level: Info, Section: "wal", Title: "e", Message: "m"},
	}
	r := NewReport("16.2", advice)

	if r.ServerVersion != "16.2" {
		t.Fatalf("ServerVersion: got %q want %q", r.ServerVersion, "16.2")
	}
	if got := r.Summary["ok"]; got != 2 {
		t.Errorf("ok count: got %d want 2", got)
	}
	if got := r.Summary["warn"]; got != 1 {
		t.Errorf("warn count: got %d want 1", got)
	}
	if got := r.Summary["bad"]; got != 1 {
		t.Errorf("bad count: got %d want 1", got)
	}
	if got := r.Summary["info"]; got != 1 {
		t.Errorf("info count: got %d want 1", got)
	}
	if got, ok := r.Summary["todo"]; ok {
		t.Errorf("todo should be absent, got %d", got)
	}
}

func TestExitCodeBad(t *testing.T) {
	r := NewReport("16.0", []Advice{
		{Level: Warn, Section: "s", Title: "t", Message: "m"},
		{Level: Bad, Section: "s", Title: "t2", Message: "m"},
	})
	if got := r.ExitCode(); got != 1 {
		t.Fatalf("ExitCode with Bad: got %d want 1", got)
	}
}

func TestExitCodeNoBad(t *testing.T) {
	r := NewReport("16.0", []Advice{
		{Level: OK, Section: "s", Title: "t", Message: "m"},
		{Level: Warn, Section: "s", Title: "t2", Message: "m"},
		{Level: Info, Section: "s", Title: "t3", Message: "m"},
	})
	if got := r.ExitCode(); got != 0 {
		t.Fatalf("ExitCode without Bad: got %d want 0", got)
	}
}

func TestExitCodeEmpty(t *testing.T) {
	r := NewReport("16.0", nil)
	if got := r.ExitCode(); got != 0 {
		t.Fatalf("ExitCode empty: got %d want 0", got)
	}
}

func adviceN(level Level, n int) []Advice {
	out := make([]Advice, n)
	for i := range out {
		out[i] = Advice{Level: level, Section: "s", Title: "t", Message: "m"}
	}
	return out
}

func TestGradeAllOK(t *testing.T) {
	r := NewReport("16", adviceN(OK, 50))
	if r.Grade() != "A" {
		t.Fatalf("all-OK: got %q want A", r.Grade())
	}
	if r.GradeLetter != "A" {
		t.Fatalf("GradeLetter not populated by NewReport: %q", r.GradeLetter)
	}
}

func TestGradeEmptyIsA(t *testing.T) {
	if g := NewReport("16", nil).Grade(); g != "A" {
		t.Fatalf("empty report: got %q want A", g)
	}
}

func TestGradeSingleBadIsC(t *testing.T) {
	// 100 - 25 = 75 → C. Any single Bad must be ≤ C.
	if g := NewReport("16", adviceN(Bad, 1)).Grade(); g != "C" {
		t.Fatalf("single Bad: got %q want C", g)
	}
}

func TestGradeTwoBadIsF(t *testing.T) {
	// 100 - 50 = 50 → F.
	if g := NewReport("16", adviceN(Bad, 2)).Grade(); g != "F" {
		t.Fatalf("two Bad: got %q want F", g)
	}
}

func TestGradeWarnBoundaries(t *testing.T) {
	if g := NewReport("16", adviceN(Warn, 1)).Grade(); g != "A" { // 92
		t.Errorf("1 warn: got %q want A", g)
	}
	if g := NewReport("16", adviceN(Warn, 3)).Grade(); g != "C" { // 76
		t.Errorf("3 warn: got %q want C", g)
	}
	if g := NewReport("16", adviceN(Warn, 5)).Grade(); g != "D" { // 60
		t.Errorf("5 warn: got %q want D", g)
	}
	if g := NewReport("16", adviceN(Warn, 6)).Grade(); g != "F" { // 52
		t.Errorf("6 warn: got %q want F", g)
	}
}

func TestGradeInfoLight(t *testing.T) {
	if g := NewReport("16", adviceN(Info, 5)).Grade(); g != "A" { // 100-10=90
		t.Errorf("5 info: got %q want A", g)
	}
	// The Info penalty is capped at 10, so 6 info → 100-10=90 → A (the cap
	// already bites at 5 info; more infos cannot push the score lower).
	if g := NewReport("16", adviceN(Info, 6)).Grade(); g != "A" { // 100-10=90
		t.Errorf("6 info: got %q want A", g)
	}
}

func TestGradeInfoPenaltyCapped(t *testing.T) {
	// 30 info would be 100-60=40→F without the cap; capped it is 100-10=90→A.
	// Informational findings alone must never fail a server.
	if g := NewReport("16", adviceN(Info, 30)).Grade(); g != "A" {
		t.Errorf("30 info (cap): got %q want A", g)
	}
}

func TestGradeUnknownSmallPenalty(t *testing.T) {
	if g := NewReport("16", adviceN(Unknown, 3)).Grade(); g != "A" { // 100-9=91
		t.Errorf("3 unknown: got %q want A", g)
	}
	// The Unknown penalty is capped at 9, so more unknowns cannot lower it
	// further: 100-9=91 → A regardless of how many beyond 3.
	if g := NewReport("16", adviceN(Unknown, 10)).Grade(); g != "A" { // 100-9=91
		t.Errorf("10 unknown (cap): got %q want A", g)
	}
}

func TestGradeManyInfosNotF(t *testing.T) {
	// 0 Bad, 1 Warn, 30 Info → 100 - 8 - min(60,10) = 82 → B.
	// Locks in the property that a dense pile of infos with one warn is a B,
	// never an F.
	advice := append(adviceN(Warn, 1), adviceN(Info, 30)...)
	r := NewReport("16", advice)
	if g := r.Grade(); g != "B" {
		t.Errorf("1 warn + 30 info: got %q want B", g)
	}
}

func TestGradeTodoNoPenalty(t *testing.T) {
	if g := NewReport("16", adviceN(Todo, 100)).Grade(); g != "A" {
		t.Errorf("Todo carries no penalty: got %q want A", g)
	}
}
