package report

import (
	"fmt"
	"io"

	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// WriteDiff writes the difference between two reports for people to read.
func WriteDiff(w io.Writer, d pgtuner.Diff, color bool) error {
	grade := func(g string) string { return paint(color, g, gradeStyle(g)...) }
	if _, err := fmt.Fprintf(w, "Grade: %s -> %s\n", grade(d.OldGrade), grade(d.NewGrade)); err != nil {
		return err
	}
	if len(d.Added)+len(d.Resolved)+len(d.Worse)+len(d.Better) == 0 {
		_, err := fmt.Fprintln(w, "\nNo findings changed.")
		return err
	}
	groups := []struct {
		title string
		items []pgtuner.Change
	}{
		{"New", d.Added}, {"Worse", d.Worse}, {"Better", d.Better}, {"Resolved", d.Resolved},
	}
	for _, g := range groups {
		if len(g.items) == 0 {
			continue
		}
		if _, err := fmt.Fprintf(w, "\n%s (%d)\n", paint(color, g.title, ansiBold), len(g.items)); err != nil {
			return err
		}
		for _, c := range g.items {
			if _, err := fmt.Fprintf(w, "  %s  %s%s  %s\n", changeTag(c, color), dbPrefix(c.Database, color), c.ID, c.Title); err != nil {
				return err
			}
		}
	}
	return nil
}

func dbPrefix(db string, color bool) string {
	if db == "" {
		return ""
	}
	return paint(color, "["+db+"] ", ansiMagenta)
}

// changeTag shows the level, or old -> new when a finding moved.
func changeTag(c pgtuner.Change, color bool) string {
	tag := func(l pgtuner.Level) string { return paint(color, "["+l.String()+"]", levelStyle(l)...) }
	switch {
	case c.Old != nil && c.New != nil:
		return tag(*c.Old) + " -> " + tag(*c.New)
	case c.New != nil:
		return tag(*c.New)
	case c.Old != nil:
		return tag(*c.Old)
	}
	return ""
}
