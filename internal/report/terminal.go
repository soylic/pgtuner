package report

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// ANSI styles. Plain output never contains them.
const (
	ansiReset   = "\033[0m"
	ansiBold    = "\033[1m"
	ansiDim     = "\033[2m"
	ansiRed     = "\033[31m"
	ansiGreen   = "\033[32m"
	ansiYellow  = "\033[33m"
	ansiBlue    = "\033[34m"
	ansiMagenta = "\033[35m"
	ansiCyan    = "\033[36m"
)

// TerminalOptions controls WriteTerminalOptions.
type TerminalOptions struct {
	Color        bool   // ANSI styles and the banner header
	SeveritySort bool   // one flat list in r.Advice order instead of sections
	Version      string // pgtuner version shown in the banner. Empty hides it
	ShowIDs      bool   // print each finding's ID, for use with -ignore
}

// paint wraps s in the given styles when color is on.
func paint(color bool, s string, styles ...string) string {
	if !color || s == "" || len(styles) == 0 {
		return s
	}
	return strings.Join(styles, "") + s + ansiReset
}

// levelStyle returns the styles for a level. Bad and warn are bold so they stand out.
func levelStyle(l pgtuner.Level) []string {
	switch l {
	case pgtuner.OK:
		return []string{ansiGreen}
	case pgtuner.Info:
		return []string{ansiCyan}
	case pgtuner.Todo:
		return []string{ansiBlue}
	case pgtuner.Warn:
		return []string{ansiBold, ansiYellow}
	case pgtuner.Bad:
		return []string{ansiBold, ansiRed}
	default:
		return []string{ansiMagenta}
	}
}

func levelTag(l pgtuner.Level) string {
	switch l {
	case pgtuner.OK:
		return "[ok]  "
	case pgtuner.Info:
		return "[info]"
	case pgtuner.Todo:
		return "[todo]"
	case pgtuner.Warn:
		return "[warn]"
	case pgtuner.Bad:
		return "[bad] "
	default:
		return "[???] "
	}
}

// gradeStyle colors a grade letter: green for A and B, yellow for C and D, red for F.
func gradeStyle(grade string) []string {
	switch grade {
	case "A", "B":
		return []string{ansiBold, ansiGreen}
	case "C", "D":
		return []string{ansiBold, ansiYellow}
	default:
		return []string{ansiBold, ansiRed}
	}
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

func writeAdvice(w io.Writer, a pgtuner.Advice, color, showID bool) error {
	tag := paint(color, levelTag(a.Level), levelStyle(a.Level)...)
	dbTag := ""
	if a.Database != "" {
		dbTag = paint(color, fmt.Sprintf("[%s]", a.Database), ansiMagenta) + " "
	}
	title := paint(color, a.Title, ansiBold)
	if _, err := fmt.Fprintf(w, "  %s  %s%s: %s\n", tag, dbTag, title, a.Message); err != nil {
		return err
	}
	if showID && a.ID != "" {
		if _, err := fmt.Fprintf(w, "          %s      %s\n", paint(color, "ID:", ansiDim), paint(color, a.ID, ansiDim)); err != nil {
			return err
		}
	}
	if a.Detail != "" {
		if _, err := fmt.Fprintf(w, "          %s %s\n", paint(color, "Detail:", ansiDim), a.Detail); err != nil {
			return err
		}
	}
	if a.DocRef != "" {
		if _, err := fmt.Fprintf(w, "          %s    %s\n", paint(color, "Ref:", ansiDim), paint(color, a.DocRef, ansiDim)); err != nil {
			return err
		}
	}
	return nil
}

// writeBanner prints the colored header box: tool version, server version,
// role when known, and the grade.
func writeBanner(w io.Writer, r *pgtuner.Report, version string) error {
	type seg struct {
		text   string
		styles []string
	}
	segs := []seg{{"pgtuner", []string{ansiBold, ansiCyan}}}
	if version != "" {
		segs = append(segs, seg{" " + version, []string{ansiDim}})
	}
	segs = append(segs, seg{"   ", nil}, seg{"PostgreSQL " + r.ServerVersion, []string{ansiBold}})
	if r.Topology != nil && r.Topology.Role != "" && r.Topology.Role != "unknown" {
		segs = append(segs, seg{"  " + strings.ReplaceAll(r.Topology.Role, "_", " "), []string{ansiDim}})
	}
	grade := r.Grade()
	segs = append(segs, seg{"   Grade ", nil}, seg{grade, gradeStyle(grade)})

	width := 0
	var line strings.Builder
	for _, s := range segs {
		width += utf8.RuneCountInString(s.text)
		line.WriteString(paint(true, s.text, s.styles...))
	}
	border := func(l, m, rr string) string {
		return paint(true, l+strings.Repeat(m, width+2)+rr, ansiDim)
	}
	edge := paint(true, "│", ansiDim)
	_, err := fmt.Fprintf(w, "%s\n%s %s %s\n%s\n\n", border("╭", "─", "╮"), edge, line.String(), edge, border("╰", "─", "╯"))
	return err
}

// levelName is the heading of a severity group.
func levelName(l pgtuner.Level) string {
	switch l {
	case pgtuner.OK:
		return "OK"
	case pgtuner.Unknown:
		return "Unknown"
	default:
		n := l.String()
		return strings.ToUpper(n[:1]) + n[1:]
	}
}

func writeLevelTitle(w io.Writer, l pgtuner.Level, n int, color bool) error {
	if color {
		_, err := fmt.Fprintf(w, "%s %s %s\n", paint(true, "▌", levelStyle(l)...), paint(true, levelName(l), levelStyle(l)...), paint(true, fmt.Sprintf("(%d)", n), ansiDim))
		return err
	}
	_, err := fmt.Fprintf(w, "=== %s (%d) ===\n", levelName(l), n)
	return err
}

func writeSectionTitle(w io.Writer, name string, color bool) error {
	if color {
		_, err := fmt.Fprintf(w, "%s %s\n", paint(true, "▌", ansiCyan), paint(true, name, ansiBold))
		return err
	}
	_, err := fmt.Fprintf(w, "=== %s ===\n", name)
	return err
}

// WriteTerminal writes a human-readable report to w. By default it groups advice
// by Section in first-seen order. With severitySort it prints one flat list in
// r.Advice order and labels a section only when it changes. With color it adds ANSI codes.
func WriteTerminal(w io.Writer, r *pgtuner.Report, color, severitySort bool) error {
	return WriteTerminalOptions(w, r, TerminalOptions{Color: color, SeveritySort: severitySort})
}

// WriteTerminalOptions is WriteTerminal with the tool version and the banner header.
// Without color the output has no banner and no ANSI codes, so scripts can parse it.
func WriteTerminalOptions(w io.Writer, r *pgtuner.Report, o TerminalOptions) error {
	color := o.Color
	if color {
		if err := writeBanner(w, r, o.Version); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintf(w, "PostgreSQL Tuning Report, server %s\n\n", r.ServerVersion); err != nil {
		return err
	}

	if o.SeveritySort {
		// r.Advice arrives sorted by severity, so each level forms one run.
		counts := map[pgtuner.Level]int{}
		for _, a := range r.Advice {
			counts[a.Level]++
		}
		var prevLevel pgtuner.Level
		prevSection := ""
		first := true
		for _, a := range r.Advice {
			if first || a.Level != prevLevel {
				if !first {
					if _, err := fmt.Fprintln(w); err != nil {
						return err
					}
				}
				if err := writeLevelTitle(w, a.Level, counts[a.Level], color); err != nil {
					return err
				}
				prevLevel, prevSection, first = a.Level, "", false
			}
			if a.Section != prevSection {
				if _, err := fmt.Fprintf(w, "  %s\n", paint(color, "("+a.Section+")", ansiDim)); err != nil {
					return err
				}
				prevSection = a.Section
			}
			if err := writeAdvice(w, a, color, o.ShowIDs); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	} else {
		sectionOrder := make([]string, 0)
		grouped := make(map[string][]pgtuner.Advice)
		for _, a := range r.Advice {
			if _, seen := grouped[a.Section]; !seen {
				sectionOrder = append(sectionOrder, a.Section)
			}
			grouped[a.Section] = append(grouped[a.Section], a)
		}

		for _, sec := range sectionOrder {
			if err := writeSectionTitle(w, sec, color); err != nil {
				return err
			}
			for _, a := range grouped[sec] {
				if err := writeAdvice(w, a, color, o.ShowIDs); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
	}

	// The summary always lists all six levels in a fixed order.
	levels := []pgtuner.Level{pgtuner.OK, pgtuner.Info, pgtuner.Todo, pgtuner.Warn, pgtuner.Bad, pgtuner.Unknown}
	if _, err := fmt.Fprintf(w, "Summary:"); err != nil {
		return err
	}
	for _, l := range levels {
		n := r.Summary[l.String()]
		cell := fmt.Sprintf("%d %s", n, l.String())
		if n == 0 {
			cell = paint(color, cell, ansiDim)
		} else {
			cell = paint(color, cell, levelStyle(l)...)
		}
		if _, err := fmt.Fprintf(w, "  %s", cell); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "   Grade: %s", paint(color, r.Grade(), gradeStyle(r.Grade())...)); err != nil {
		return err
	}
	if r.Ignored > 0 {
		if _, err := fmt.Fprintf(w, "   %s", paint(color, fmt.Sprintf("(%d ignored)", r.Ignored), ansiDim)); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}
