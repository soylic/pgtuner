package report

import (
	"encoding/csv"
	"io"

	"github.com/soylic/pgtuner/pkg/pgtuner"
)

// WriteCSV writes one row per advice item in r.Advice order, after a header row
// database,section,level,title,message,detail,doc_ref. The grade and summary are
// not included. Use the JSON or terminal output to get them.
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
