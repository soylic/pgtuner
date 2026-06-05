package report

import (
	"bytes"
	"testing"

	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

func TestWriteJSON(t *testing.T) {
	advice := []pgtuner.Advice{
		{
			Level:   pgtuner.Warn,
			Section: "memory",
			Title:   "shared_buffers low",
			Message: "shared_buffers is below 25% of RAM.",
			Detail:  "Current value is 128MB; recommended minimum is 512MB.",
			DocRef:  "https://www.postgresql.org/docs/current/runtime-config-resource.html",
		},
		{
			Level:   pgtuner.OK,
			Section: "security",
			Title:   "ssl enabled",
			Message: "SSL is enabled.",
		},
	}
	rep := pgtuner.NewReport("16.2", advice)

	var buf bytes.Buffer
	if err := WriteJSON(&buf, rep); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	const want = `{
  "server_version": "16.2",
  "advice": [
    {
      "level": "warn",
      "section": "memory",
      "title": "shared_buffers low",
      "message": "shared_buffers is below 25% of RAM.",
      "detail": "Current value is 128MB; recommended minimum is 512MB.",
      "doc_ref": "https://www.postgresql.org/docs/current/runtime-config-resource.html"
    },
    {
      "level": "ok",
      "section": "security",
      "title": "ssl enabled",
      "message": "SSL is enabled."
    }
  ],
  "summary": {
    "ok": 1,
    "warn": 1
  },
  "grade": "A"
}
`
	got := buf.String()
	if got != want {
		t.Errorf("JSON mismatch.\ngot:\n%s\nwant:\n%s", got, want)
	}
}
