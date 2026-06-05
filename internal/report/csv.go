package report

import (
	"encoding/csv"
	"io"

	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// WriteCSV writes the report's advice as CSV to w. The header row is
// database,section,level,title,message,detail,doc_ref and there is one row per
// advice item in r.Advice, preserving the caller's order (the caller is
// expected to have already applied any sort/min-level filtering).
//
// The grade and per-level summary are intentionally NOT included: CSV output is
// a clean, importable table of findings. Read the grade/summary from the JSON
// or terminal renderer when those are needed.
func WriteCSV(w io.Writer, r *pgtuner.Report) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"database", "section", "level", "title", "message", "detail", "doc_ref"}); err != nil {
		return err
	}
	for _, a := range r.Advice {
		row := []string{a.Database, a.Section, a.Level.String(), a.Title, a.Message, a.Detail, a.DocRef}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
