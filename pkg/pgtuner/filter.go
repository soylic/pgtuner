package pgtuner

import (
	"fmt"
	"path"
)

// MatchID reports whether an advice ID matches a pattern. The pattern is an
// exact ID, or a path.Match pattern such as "connection.*" or "*.query-failed".
func MatchID(pattern, id string) bool {
	if pattern == id {
		return true
	}
	ok, err := path.Match(pattern, id)
	return err == nil && ok
}

// ValidateIDPattern returns an error when the pattern is not a valid path.Match pattern.
func ValidateIDPattern(pattern string) error {
	if _, err := path.Match(pattern, ""); err != nil {
		return fmt.Errorf("invalid pattern %q: %w", pattern, err)
	}
	return nil
}

// FilterIgnored drops advice whose ID matches any pattern. It returns the kept
// advice in input order and the number dropped. Advice with no ID is never dropped.
func FilterIgnored(advice []Advice, patterns []string) ([]Advice, int) {
	if len(patterns) == 0 {
		return advice, 0
	}
	kept := make([]Advice, 0, len(advice))
	dropped := 0
	for _, a := range advice {
		skip := false
		if a.ID != "" {
			for _, p := range patterns {
				if MatchID(p, a.ID) {
					skip = true
					break
				}
			}
		}
		if skip {
			dropped++
			continue
		}
		kept = append(kept, a)
	}
	return kept, dropped
}

// failRank orders the levels that -fail-on accepts. Unknown is not one of them.
func failRank(l Level) int {
	switch l {
	case Info:
		return 1
	case Todo:
		return 2
	case Warn:
		return 3
	case Bad:
		return 4
	default:
		return 0
	}
}

// ParseFailOn reads the -fail-on value: none, info, todo, warn or bad.
// For none it returns ok=false.
func ParseFailOn(s string) (min Level, ok bool, err error) {
	switch s {
	case "none":
		return OK, false, nil
	case "info", "todo", "warn", "bad":
		l, _ := ParseLevel(s)
		return l, true, nil
	default:
		return OK, false, fmt.Errorf("invalid value %q (use none, info, todo, warn or bad)", s)
	}
}

// ExitCodeAt returns 1 when any advice is at min or worse (Unknown does not count), else 0.
func (r *Report) ExitCodeAt(min Level) int {
	for _, a := range r.Advice {
		if failRank(a.Level) >= failRank(min) && failRank(a.Level) > 0 {
			return 1
		}
	}
	return 0
}
