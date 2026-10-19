package checks

import (
	"context"
	"testing"

	"github.com/soylic/pgtuner/internal/osinspect"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

type stubCheck struct {
	clusterScoped
	name string
}

func (s stubCheck) Name() string { return s.name }
func (s stubCheck) Run(ctx context.Context, p *Probe) []pgtuner.Advice {
	return []pgtuner.Advice{{Level: pgtuner.OK, Section: s.name}}
}

func TestRegistryRunsInOrder(t *testing.T) {
	r := Registry{stubCheck{name: "a"}, stubCheck{name: "b"}}
	got := r.RunAll(context.Background(), &Probe{OS: osinspect.NoopInspector{}})
	if len(got) != 2 || got[0].Section != "a" || got[1].Section != "b" {
		t.Fatalf("order wrong: %+v", got)
	}
}
