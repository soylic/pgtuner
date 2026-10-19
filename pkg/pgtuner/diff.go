package pgtuner

import "sort"

// Change is one finding that differs between two reports.
type Change struct {
	ID       string `json:"id"`
	Database string `json:"database,omitempty"`
	Title    string `json:"title"`
	Old      *Level `json:"old,omitempty"` // nil when the finding is new
	New      *Level `json:"new,omitempty"` // nil when the finding is gone
}

// Diff says what changed from an old report to a new one.
type Diff struct {
	OldGrade string   `json:"old_grade"`
	NewGrade string   `json:"new_grade"`
	Added    []Change `json:"added,omitempty"`    // in the new report only
	Resolved []Change `json:"resolved,omitempty"` // in the old report only
	Worse    []Change `json:"worse,omitempty"`    // same finding, higher severity
	Better   []Change `json:"better,omitempty"`   // same finding, lower severity
}

type diffKey struct{ db, id string }

// severityWeight is rank with Unknown placed below Info, so a finding that
// turns into Unknown does not look like a regression.
func severityWeight(l Level) int {
	if l == Unknown {
		return -1
	}
	return failRank(l)
}

// worstByKey keeps, for each (database, ID), the worst level and the title of that finding.
func worstByKey(r *Report) map[diffKey]Advice {
	out := map[diffKey]Advice{}
	for _, a := range r.Advice {
		id := a.ID
		if id == "" {
			id = "title:" + a.Title
		}
		k := diffKey{a.Database, id}
		if cur, ok := out[k]; !ok || severityWeight(a.Level) > severityWeight(cur.Level) {
			out[k] = a
		}
	}
	return out
}

// Compare matches findings by database and ID. A finding that appears several
// times with one ID (one per slot or per table) counts once, at its worst level.
// Findings at OK level count as present, so ok to warn shows as worse.
func Compare(old, new *Report) Diff {
	o, n := worstByKey(old), worstByKey(new)
	d := Diff{OldGrade: old.Grade(), NewGrade: new.Grade()}
	lv := func(l Level) *Level { return &l }
	for k, a := range n {
		prev, ok := o[k]
		c := Change{ID: a.ID, Database: a.Database, Title: a.Title, New: lv(a.Level)}
		switch {
		case !ok:
			d.Added = append(d.Added, c)
		case severityWeight(a.Level) > severityWeight(prev.Level):
			c.Old = lv(prev.Level)
			d.Worse = append(d.Worse, c)
		case severityWeight(a.Level) < severityWeight(prev.Level):
			c.Old = lv(prev.Level)
			d.Better = append(d.Better, c)
		}
	}
	for k, a := range o {
		if _, ok := n[k]; !ok {
			d.Resolved = append(d.Resolved, Change{ID: a.ID, Database: a.Database, Title: a.Title, Old: lv(a.Level)})
		}
	}
	for _, s := range [][]Change{d.Added, d.Resolved, d.Worse, d.Better} {
		sort.Slice(s, func(i, j int) bool {
			if s[i].ID != s[j].ID {
				return s[i].ID < s[j].ID
			}
			return s[i].Database < s[j].Database
		})
	}
	return d
}

// Regressed reports whether the new report has a finding that is new or worse at min or above.
func (d Diff) Regressed(min Level) bool {
	for _, c := range append(append([]Change{}, d.Added...), d.Worse...) {
		if c.New != nil && failRank(*c.New) >= failRank(min) && failRank(*c.New) > 0 {
			return true
		}
	}
	return false
}
