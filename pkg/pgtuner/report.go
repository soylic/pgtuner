package pgtuner

import "time"

// Options configures an Analyze run.
type Options struct {
	DSN          string    // libpq DSN; empty falls back to PG* env / ~/.pgpass
	OSInspect    bool      // when true and running on Linux, gather OS facts
	Now          time.Time // injected clock for the version check; zero = time.Now()
	AllDatabases bool      // when true, run per-database checks against every user database
}

// Report is the result of analyzing a server.
type Report struct {
	ServerVersion string         `json:"server_version"`
	Advice        []Advice       `json:"advice"`
	Summary       map[string]int `json:"summary"` // level name -> count
	GradeLetter   string         `json:"grade"`   // overall letter grade A–F (see computeGrade); read via Grade()
	// Databases lists the user databases analyzed under -all-databases, in the
	// order they were inspected. Empty for a default (single-DB) run.
	// Per-database advice is also tagged via Advice.Database.
	Databases []string `json:"databases,omitempty"`
}

// ExitCode returns 1 if any advice is Bad-level, else 0.
func (r *Report) ExitCode() int {
	for _, a := range r.Advice {
		if a.Level == Bad {
			return 1
		}
	}
	return 0
}

// NewReport builds a Report from a server version string and advice list,
// computing the per-level summary and overall grade.
func NewReport(version string, advice []Advice) *Report {
	sum := make(map[string]int)
	for _, a := range advice {
		sum[a.Level.String()]++
	}
	r := &Report{ServerVersion: version, Advice: advice, Summary: sum}
	r.GradeLetter = r.computeGrade()
	return r
}

// Grade returns the overall letter grade (A–F) for the report. It is a pure
// function of the report's per-level Summary; see computeGrade for the formula.
func (r *Report) Grade() string {
	if r.GradeLetter != "" {
		return r.GradeLetter
	}
	return r.computeGrade()
}

// Penalty weights for the grade formula. Each finding subtracts points from a
// 100-point starting score, encoding severity priority:
//   - Bad: a dangerous/high-risk setting, weighted heavily.
//   - Warn: a suboptimal setting, weighted moderately.
//   - Info: a low-priority suggestion, weighted lightly.
//   - Unknown: an undetermined check; a small penalty, because an unknown is a
//     gap in coverage we cannot rule out — not a confirmed problem.
//   - OK and Todo: no penalty. OK is a pass; Todo is a tracked improvement that
//     does not by itself degrade the running server's health.
const (
	gradePenaltyBad     = 25
	gradePenaltyWarn    = 8
	gradePenaltyInfo    = 2
	gradePenaltyUnknown = 3
)

// Caps on the aggregate Info and Unknown penalties. Info and Unknown findings
// are low-priority — a report dense with informational suggestions or
// undetermined coverage gaps must not be able to fail purely on their volume.
// Capping the combined penalty keeps Bad and Warn as the dominant signals.
const (
	gradeInfoPenaltyCap    = 10
	gradeUnknownPenaltyCap = 9
)

// computeGrade implements the grade formula.
//
// Score starts at 100 and loses points per finding, with the aggregate Info and
// Unknown penalties capped so informational findings cannot dominate:
//
//	infoPenalty    = min(2*Info, 10)
//	unknownPenalty = min(3*Unknown, 9)
//	score = 100 - 25*Bad - 8*Warn - infoPenalty - unknownPenalty   (clamped to [0,100])
//
// Todo and OK carry no penalty. The score maps to a letter:
//
//	A: score >= 90
//	B: score >= 80
//	C: score >= 70
//	D: score >= 60
//	F: score <  60
//
// Consequences worth noting:
//   - An all-OK report scores 100 → A.
//   - A single Bad alone drops the score to 75 → C (so "any Bad ⇒ grade ≤ C").
//     Two Bads (50) → F. The heavy Bad weight ensures dangerous settings cannot
//     hide behind otherwise-clean output.
//   - The Info and Unknown caps mean that with no Bad and few Warn, a server
//     cannot score F on informational findings alone: the worst either category
//     can subtract is its cap (10 and 9 respectively).
func (r *Report) computeGrade() string {
	c := func(l Level) int { return r.Summary[l.String()] }
	infoPenalty := gradePenaltyInfo * c(Info)
	if infoPenalty > gradeInfoPenaltyCap {
		infoPenalty = gradeInfoPenaltyCap
	}
	unknownPenalty := gradePenaltyUnknown * c(Unknown)
	if unknownPenalty > gradeUnknownPenaltyCap {
		unknownPenalty = gradeUnknownPenaltyCap
	}
	score := 100 -
		gradePenaltyBad*c(Bad) -
		gradePenaltyWarn*c(Warn) -
		infoPenalty -
		unknownPenalty
	if score < 0 {
		score = 0
	}
	switch {
	case score >= 90:
		return "A"
	case score >= 80:
		return "B"
	case score >= 70:
		return "C"
	case score >= 60:
		return "D"
	default:
		return "F"
	}
}
