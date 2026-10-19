package pgtuner

import "time"

// Options configures an Analyze run.
type Options struct {
	DSN          string    // libpq DSN. Empty uses PG* env and ~/.pgpass
	OSInspect    bool      // collect OS facts (Linux only)
	Now          time.Time // clock for the version check. Zero means time.Now()
	AllDatabases bool      // run per-database checks on every user database
	// Skip lists check names to leave out. Only, when set, lists the only checks to run.
	// Names are the Name() values, such as replication or autovacuum_settings.
	Skip []string
	Only []string
	// Ignore drops findings whose ID matches. A pattern may end in * (connection.*).
	// Ignored findings count in Report.Ignored, not in the summary or the grade.
	Ignore []string
	// StatementTimeout limits each query. Zero means 30 seconds. A negative value turns the limit off.
	StatementTimeout time.Duration
	// PatroniURL is the base URL of a Patroni REST API, such as http://pg1:8008.
	// When set, pgtuner asks it for the member list and reports Patroni as certain.
	PatroniURL string
}

// Report is the result of analyzing a server.
type Report struct {
	ServerVersion string         `json:"server_version"`
	Advice        []Advice       `json:"advice"`
	Summary       map[string]int `json:"summary"` // level name -> count
	GradeLetter   string         `json:"grade"`   // letter grade A to F (see computeGrade). Read it with Grade()
	// Databases lists the databases analyzed under -all-databases, in inspection order.
	Databases []string `json:"databases,omitempty"`
	// Topology and Flavor describe the analyzed server. Both are best-effort and may be nil.
	Topology *Topology `json:"topology,omitempty"`
	Flavor   *Flavor   `json:"flavor,omitempty"`
	// Ignored counts the findings dropped by Options.Ignore.
	Ignored int `json:"ignored,omitempty"`
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

// NewReport builds a Report and computes its summary and grade.
func NewReport(version string, advice []Advice) *Report {
	sum := make(map[string]int)
	for _, a := range advice {
		sum[a.Level.String()]++
	}
	r := &Report{ServerVersion: version, Advice: advice, Summary: sum}
	r.GradeLetter = r.computeGrade()
	return r
}

// Grade returns the letter grade. It depends only on Summary (see computeGrade).
func (r *Report) Grade() string {
	if r.GradeLetter != "" {
		return r.GradeLetter
	}
	return r.computeGrade()
}

// Penalty weights for the grade formula, subtracted from a score of 100.
// Unknown has a small penalty because it is a coverage gap, not a confirmed problem.
// OK and Todo have no penalty.
const (
	gradePenaltyBad     = 25
	gradePenaltyWarn    = 8
	gradePenaltyInfo    = 2
	gradePenaltyUnknown = 3
)

// Caps on the total Info and Unknown penalties keep Bad and Warn dominant.
const (
	gradeInfoPenaltyCap    = 10
	gradeUnknownPenaltyCap = 9
)

// computeGrade implements the grade formula:
//
//	infoPenalty    = min(2*Info, 10)
//	unknownPenalty = min(3*Unknown, 9)
//	score = 100 - 25*Bad - 8*Warn - infoPenalty - unknownPenalty   (clamped to [0,100])
//
// The score maps to a letter:
//
//	A: score >= 90
//	B: score >= 80
//	C: score >= 70
//	D: score >= 60
//	F: score <  60
//
// One Bad scores 75 (C), so any Bad gives a grade of C or lower. Two Bads score 50 (F).
// With no Bad and few Warn, Info and Unknown findings alone cannot give an F.
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
