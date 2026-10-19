// Package pgtuner exposes the public tuning-advisor API.
package pgtuner

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Level is the severity of an Advice finding.
type Level int

const (
	OK      Level = iota // The setting is within recommended values.
	Info                 // A low-priority suggestion.
	Todo                 // An improvement to schedule.
	Warn                 // A suboptimal configuration.
	Bad                  // A dangerous or high-risk setting.
	Unknown              // A check could not determine a result.
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

func (l Level) MarshalJSON() ([]byte, error) {
	return json.Marshal(l.String())
}

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
	// ID names the condition that produced the finding, as <check>.<condition>.
	// It does not change when the wording or the numbers change, so scripts
	// can match on it. Examples: connection.ssl-disabled, wal.fsync-off.
	ID      string `json:"id"`
	Level   Level  `json:"level"`
	Section string `json:"section"`
	Title   string `json:"title"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
	DocRef  string `json:"doc_ref,omitempty"` // PG docs URL for the threshold
	// Database is set only for per-database checks under -all-databases.
	Database string `json:"database,omitempty"`
}

// rank maps a Level to its severity for filtering. Unknown ranks above Bad (see WorstLevel).
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
// Bad so a check that could not finish is not hidden. Report.ExitCode looks only at Bad.
func WorstLevel(a []Advice) Level {
	worst := OK
	for _, x := range a {
		if rank(x.Level) > rank(worst) {
			worst = x.Level
		}
	}
	return worst
}

// ParseLevel converts a lowercase level name to a Level.
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
		return OK, fmt.Errorf("invalid level %q (use one of ok/info/todo/warn/bad/unknown)", s)
	}
}

// FilterByMinLevel keeps advice whose rank is at least the rank of min, in input order.
// The ranking is Unknown > Bad > Warn > Todo > Info > OK. With min == OK, nothing is dropped.
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

// SortBySeverity returns a stable copy of advice ordered Bad, Warn, Todo, Info, OK, Unknown.
// Unknown sorts last so undetermined checks do not fill the top of the report.
// This differs from rank(), where Unknown is highest.
func SortBySeverity(advice []Advice) []Advice {
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
