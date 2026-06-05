// Package pgtuner exposes the public tuning-advisor API.
package pgtuner

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Level represents the severity of an Advice finding.
type Level int

const (
	OK      Level = iota // OK: setting is within recommended parameters.
	Info                 // Info: low-priority suggestion.
	Todo                 // Todo: improvement to schedule.
	Warn                 // Warn: suboptimal configuration.
	Bad                  // Bad: dangerous or high-risk setting.
	Unknown              // Unknown: a check could not determine a result.
)

func (l Level) String() string {
	switch l {
	case OK:
		return "ok"
	case Info:
		return "info"
	case Todo:
		return "todo"
	case Warn:
		return "warn"
	case Bad:
		return "bad"
	default:
		return "unknown"
	}
}

// MarshalJSON encodes the level as its lowercase string name.
func (l Level) MarshalJSON() ([]byte, error) {
	return json.Marshal(l.String())
}

// UnmarshalJSON decodes a level from its lowercase string name.
func (l *Level) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	switch s {
	case "ok":
		*l = OK
	case "info":
		*l = Info
	case "todo":
		*l = Todo
	case "warn":
		*l = Warn
	case "bad":
		*l = Bad
	case "unknown":
		*l = Unknown
	default:
		return fmt.Errorf("unknown level %q", s)
	}
	return nil
}

// Advice is one finding from one check.
type Advice struct {
	Level   Level  `json:"level"`
	Section string `json:"section"`
	Title   string `json:"title"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
	DocRef  string `json:"doc_ref,omitempty"` // PG docs URL the threshold came from
	// Database is the user database this finding pertains to. It is set only for
	// per-database checks run under -all-databases; cluster-wide findings and
	// default (single-DB) runs leave it empty.
	Database string `json:"database,omitempty"`
}

// rank maps a Level to its severity rank used for ordering and filtering.
// Higher means more severe. Unknown ranks above Bad on purpose (see WorstLevel).
//
// Ranking (highest → lowest): Unknown(5) > Bad(4) > Warn(3) > Todo(2) > Info(1) > OK(0).
func rank(l Level) int {
	switch l {
	case OK:
		return 0
	case Info:
		return 1
	case Todo:
		return 2
	case Warn:
		return 3
	case Bad:
		return 4
	case Unknown:
		return 5
	default:
		return 5
	}
}

// WorstLevel returns the highest-severity level present. Unknown ranks above
// Bad on purpose: a check that could not complete is surfaced prominently
// rather than silently treated as low severity (fail-loud). Note that
// Report.ExitCode keys only off Bad, so an Unknown alone does not fail the CLI.
//
// Ranking (highest → lowest): Unknown > Bad > Warn > Todo > Info > OK.
func WorstLevel(a []Advice) Level {
	worst := OK
	for _, x := range a {
		if rank(x.Level) > rank(worst) {
			worst = x.Level
		}
	}
	return worst
}

// ParseLevel converts a lowercase level name to a Level. It returns an error
// for any string that is not one of ok/info/todo/warn/bad/unknown.
func ParseLevel(s string) (Level, error) {
	switch s {
	case "ok":
		return OK, nil
	case "info":
		return Info, nil
	case "todo":
		return Todo, nil
	case "warn":
		return Warn, nil
	case "bad":
		return Bad, nil
	case "unknown":
		return Unknown, nil
	default:
		return OK, fmt.Errorf("invalid level %q (want one of ok/info/todo/warn/bad/unknown)", s)
	}
}

// FilterByMinLevel returns the subset of advice whose severity rank is at least
// the rank of min. With min == OK (rank 0) every item is kept (no filtering).
// The rank ordering matches WorstLevel: Unknown > Bad > Warn > Todo > Info > OK,
// so e.g. min == Warn keeps Warn, Bad, and Unknown while dropping Todo/Info/OK.
// The returned slice preserves the input order (returns a nil slice when
// nothing matches).
func FilterByMinLevel(advice []Advice, min Level) []Advice {
	minRank := rank(min)
	if minRank == 0 {
		return advice
	}
	var out []Advice
	for _, a := range advice {
		if rank(a.Level) >= minRank {
			out = append(out, a)
		}
	}
	return out
}

// SortBySeverity returns a copy of advice ordered by descending severity:
// Bad → Warn → Todo → Info → OK → Unknown. The sort is stable, so items of
// equal severity keep their original relative order. Note this ordering differs
// from rank() used for filtering: Unknown sorts LAST here (so undetermined
// checks don't dominate the top of a severity-sorted report) even though it
// ranks highest for WorstLevel/filtering purposes. The input is not mutated.
func SortBySeverity(advice []Advice) []Advice {
	// Display order weight: lower sorts first.
	weight := func(l Level) int {
		switch l {
		case Bad:
			return 0
		case Warn:
			return 1
		case Todo:
			return 2
		case Info:
			return 3
		case OK:
			return 4
		case Unknown:
			return 5
		default:
			return 5
		}
	}
	out := make([]Advice, len(advice))
	copy(out, advice)
	sort.SliceStable(out, func(i, j int) bool {
		return weight(out[i].Level) < weight(out[j].Level)
	})
	return out
}
