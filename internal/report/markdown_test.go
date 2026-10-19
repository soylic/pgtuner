package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/soylic/pgtuner/pkg/pgtuner"
)

func TestWriteMarkdown(t *testing.T) {
	advice := []pgtuner.Advice{
		{
			Level:   pgtuner.Warn,
			Section: "memory",
			Title:   "shared_buffers low",
			Message: "shared_buffers is below 25% of RAM.",
			Detail:  "Current value is 128MB.",
			DocRef:  "https://www.postgresql.org/docs/current/runtime-config-resource.html",
		},
		{
			Level:   pgtuner.Info,
			Section: "memory",
			Title:   "work_mem | default",
			Message: "work_mem is at\ndefault.",
		},
		{
			Level:    pgtuner.OK,
			Section:  "security",
			Title:    "ssl enabled",
			Message:  "SSL is enabled.",
			Database: "appdb",
		},
	}
	rep := pgtuner.NewReport("16.2", advice)

	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, rep); err != nil {
		t.Fatalf("WriteMarkdown: %v", err)
	}
	got := buf.String()

	checks := []string{
		"# PostgreSQL Tuning Report, server 16.2",
		"**Grade: A**",
		"Summary: 1 ok · 1 info · 0 todo · 1 warn · 0 bad · 0 unknown",
		"### memory",
		"| Level | Title | Message |",
		"|-------|-------|---------|",
		"| warn | shared_buffers low | shared_buffers is below 25% of RAM. |",
		"| info | work_mem \\| default | work_mem is at default. |",
		"- _Detail:_ Current value is 128MB.",
		"- _Ref:_ https://www.postgresql.org/docs/current/runtime-config-resource.html",
		"### security",
		"| ok | [appdb] ssl enabled | SSL is enabled. |",
	}
	for _, c := range checks {
		if !strings.Contains(got, c) {
			t.Errorf("markdown output missing %q.\nfull output:\n%s", c, got)
		}
	}

	// A newline in a message must not appear inside the table row.
	if strings.Contains(got, "work_mem is at\ndefault.") {
		t.Errorf("newline in message was not collapsed:\n%s", got)
	}
}
