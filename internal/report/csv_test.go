package report

import (
	"bytes"
	"testing"

	"github.com/soylic/pgtuner/pkg/pgtuner"
)

func TestWriteCSV(t *testing.T) {
	advice := []pgtuner.Advice{
		{
			ID:      "memory.shared-buffers-low",
			Level:   pgtuner.Warn,
			Section: "memory",
			Title:   "shared_buffers low",
			Message: "shared_buffers is below 25% of RAM, recommend raising.",
			Detail:  "Current value is 128MB.",
			DocRef:  "https://www.postgresql.org/docs/current/runtime-config-resource.html",
		},
		{
			ID:       "cache.hit-ratio-low",
			Level:    pgtuner.Info,
			Section:  "cache",
			Title:    "hit ratio",
			Message:  `Buffer cache hit ratio is "98.85%", below target.`,
			Database: "appdb",
		},
		{
			ID:      "security.ssl-ok",
			Level:   pgtuner.OK,
			Section: "security",
			Title:   "ssl enabled",
			Message: "SSL is enabled.",
		},
	}
	rep := pgtuner.NewReport("16.2", advice)

	var buf bytes.Buffer
	if err := WriteCSV(&buf, rep); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}

	const want = `database,section,level,id,title,message,detail,doc_ref
,memory,warn,memory.shared-buffers-low,shared_buffers low,"shared_buffers is below 25% of RAM, recommend raising.",Current value is 128MB.,https://www.postgresql.org/docs/current/runtime-config-resource.html
appdb,cache,info,cache.hit-ratio-low,hit ratio,"Buffer cache hit ratio is ""98.85%"", below target.",,
,security,ok,security.ssl-ok,ssl enabled,SSL is enabled.,,
`
	got := buf.String()
	if got != want {
		t.Errorf("CSV mismatch.\ngot:\n%s\nwant:\n%s", got, want)
	}
}
