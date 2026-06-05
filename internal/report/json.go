// Package report provides output renderers for a pgtuner Report.
package report

import (
	"encoding/json"
	"io"

	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// WriteJSON writes the report as indented JSON.
func WriteJSON(w io.Writer, r *pgtuner.Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}
