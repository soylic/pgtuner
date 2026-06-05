package report

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/term"

	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// ANSI color codes.
const (
	ansiReset   = "\033[0m"
	ansiRed     = "\033[31m"
	ansiYellow  = "\033[33m"
	ansiCyan    = "\033[36m"
	ansiGreen   = "\033[32m"
	ansiBlue    = "\033[34m"
	ansiMagenta = "\033[35m"
)

// levelGlyph returns the tag and optional ANSI color for a level.
func levelGlyph(l pgtuner.Level, color bool) (tag, colorCode string) {
	switch l {
	case pgtuner.OK:
		tag, colorCode = "[ok]  ", ansiGreen
	case pgtuner.Info:
		tag, colorCode = "[info]", ansiCyan
	case pgtuner.Todo:
		tag, colorCode = "[todo]", ansiBlue
	case pgtuner.Warn:
		tag, colorCode = "[warn]", ansiYellow
	case pgtuner.Bad:
		tag, colorCode = "[bad] ", ansiRed
	default:
		tag, colorCode = "[???] ", ansiMagenta
	}
	if !color {
		colorCode = ""
	}
	return tag, colorCode
}

// ColorEnabled reports whether color output is appropriate for f.
// It returns false when the NO_COLOR environment variable is set (to any value)
// or when f is not a terminal. See https://no-color.org/
func ColorEnabled(f *os.File) bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// writeAdvice renders a single advice item with its level glyph, optional
// database tag, detail, and doc reference.
func writeAdvice(w io.Writer, a pgtuner.Advice, color bool) error {
	tag, col := levelGlyph(a.Level, color)
	dbTag := ""
	if a.Database != "" {
		dbTag = fmt.Sprintf("[%s] ", a.Database)
	}
	var line string
	if color {
		line = fmt.Sprintf("  %s%s%s  %s%s — %s\n", col, tag, ansiReset, dbTag, a.Title, a.Message)
	} else {
		line = fmt.Sprintf("  %s  %s%s — %s\n", tag, dbTag, a.Title, a.Message)
	}
	if _, err := fmt.Fprint(w, line); err != nil {
		return err
	}
	if a.Detail != "" {
		if _, err := fmt.Fprintf(w, "          Detail: %s\n", a.Detail); err != nil {
			return err
		}
	}
	if a.DocRef != "" {
		if _, err := fmt.Fprintf(w, "          Ref:    %s\n", a.DocRef); err != nil {
			return err
		}
	}
	return nil
}

// WriteTerminal writes a human-readable report to w. When severitySort is
// false (the default) advice is grouped by Section (preserving first-seen
// order); when true it is rendered as a single flat list in the order of
// r.Advice (the caller is expected to have severity-sorted it). A "(section)"
// label is printed only when the section changes between consecutive items, so
// runs of same-section items are not repeatedly labelled. When color is true,
// ANSI codes highlight levels.
func WriteTerminal(w io.Writer, r *pgtuner.Report, color, severitySort bool) error {
	// Header.
	if _, err := fmt.Fprintf(w, "PostgreSQL Tuning Report — server %s\n\n", r.ServerVersion); err != nil {
		return err
	}

	if severitySort {
		// Flat severity-ordered list. Print the section label only when it
		// changes from the previous item's section, so a run of items in the
		// same section is not prefixed by a repeated "(section)" line.
		if _, err := fmt.Fprintf(w, "=== Findings (by severity) ===\n"); err != nil {
			return err
		}
		prevSection := ""
		first := true
		for _, a := range r.Advice {
			if first || a.Section != prevSection {
				if _, err := fmt.Fprintf(w, "  (%s)\n", a.Section); err != nil {
					return err
				}
				prevSection = a.Section
				first = false
			}
			if err := writeAdvice(w, a, color); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	} else {
		// Gather section order and group advice.
		sectionOrder := make([]string, 0)
		grouped := make(map[string][]pgtuner.Advice)
		for _, a := range r.Advice {
			if _, seen := grouped[a.Section]; !seen {
				sectionOrder = append(sectionOrder, a.Section)
			}
			grouped[a.Section] = append(grouped[a.Section], a)
		}

		// Print each section.
		for _, sec := range sectionOrder {
			if _, err := fmt.Fprintf(w, "=== %s ===\n", sec); err != nil {
				return err
			}
			for _, a := range grouped[sec] {
				if err := writeAdvice(w, a, color); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
	}

	// Summary line — always print all six levels in a fixed order.
	levels := []pgtuner.Level{pgtuner.OK, pgtuner.Info, pgtuner.Todo, pgtuner.Warn, pgtuner.Bad, pgtuner.Unknown}
	if _, err := fmt.Fprintf(w, "Summary:"); err != nil {
		return err
	}
	for _, l := range levels {
		n := r.Summary[l.String()]
		if _, err := fmt.Fprintf(w, "  %d %s", n, l.String()); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "   Grade: %s\n", r.Grade())
	return err
}
