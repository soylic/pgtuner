package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// mdEscape makes s safe to place inside a Markdown table cell: pipe characters
// are escaped so they don't start a new column, and newlines are collapsed to
// spaces so a cell stays on one line.
func mdEscape(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

// WriteMarkdown writes a Markdown rendering of the report to w, suitable for
// pasting into a PR or issue. It opens with a title, an overall grade line, and
// a summary line, then groups advice by Section in first-seen order (matching
// WriteTerminal). Each section is a heading followed by a table of
// Level/Title/Message; any Detail or DocRef is appended as indented sub-lines
// under the row. Per-database findings (under -all-databases) carry their
// database name as a "[db]" prefix on the title, the same convention the
// terminal renderer uses.
func WriteMarkdown(w io.Writer, r *pgtuner.Report) error {
	if _, err := fmt.Fprintf(w, "# PostgreSQL Tuning Report — server %s\n\n", r.ServerVersion); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "**Grade: %s**\n\n", r.Grade()); err != nil {
		return err
	}

	// Summary line — all six levels in fixed order, matching the terminal footer.
	levels := []pgtuner.Level{pgtuner.OK, pgtuner.Info, pgtuner.Todo, pgtuner.Warn, pgtuner.Bad, pgtuner.Unknown}
	parts := make([]string, 0, len(levels))
	for _, l := range levels {
		parts = append(parts, fmt.Sprintf("%d %s", r.Summary[l.String()], l.String()))
	}
	if _, err := fmt.Fprintf(w, "Summary: %s\n\n", strings.Join(parts, " · ")); err != nil {
		return err
	}

	// Group advice by section, preserving first-seen order.
	sectionOrder := make([]string, 0)
	grouped := make(map[string][]pgtuner.Advice)
	for _, a := range r.Advice {
		if _, seen := grouped[a.Section]; !seen {
			sectionOrder = append(sectionOrder, a.Section)
		}
		grouped[a.Section] = append(grouped[a.Section], a)
	}

	for _, sec := range sectionOrder {
		if _, err := fmt.Fprintf(w, "### %s\n\n", sec); err != nil {
			return err
		}
		if _, err := fmt.Fprint(w, "| Level | Title | Message |\n|-------|-------|---------|\n"); err != nil {
			return err
		}
		// Collect any sub-lines (Detail/Ref) to print after the table so the
		// table itself stays well-formed.
		type subline struct {
			title string
			lines []string
		}
		var subs []subline
		for _, a := range grouped[sec] {
			title := a.Title
			if a.Database != "" {
				title = fmt.Sprintf("[%s] %s", a.Database, a.Title)
			}
			if _, err := fmt.Fprintf(w, "| %s | %s | %s |\n",
				mdEscape(a.Level.String()), mdEscape(title), mdEscape(a.Message)); err != nil {
				return err
			}
			var lines []string
			if a.Detail != "" {
				lines = append(lines, fmt.Sprintf("- _Detail:_ %s", mdEscape(a.Detail)))
			}
			if a.DocRef != "" {
				lines = append(lines, fmt.Sprintf("- _Ref:_ %s", mdEscape(a.DocRef)))
			}
			if len(lines) > 0 {
				subs = append(subs, subline{title: title, lines: lines})
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		for _, s := range subs {
			if _, err := fmt.Fprintf(w, "**%s**\n", s.title); err != nil {
				return err
			}
			for _, l := range s.lines {
				if _, err := fmt.Fprintln(w, l); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
	}

	return nil
}
