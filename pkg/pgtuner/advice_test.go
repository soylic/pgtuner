package pgtuner

import (
	"encoding/json"
	"testing"
)

func TestLevelString(t *testing.T) {
	if Bad.String() != "bad" {
		t.Fatalf("got %q", Bad.String())
	}
}

func TestAdviceWorstLevel(t *testing.T) {
	a := []Advice{{Level: OK}, {Level: Warn}, {Level: Bad}, {Level: Info}}
	if got := WorstLevel(a); got != Bad {
		t.Fatalf("got %v want Bad", got)
	}
}

func TestWorstLevelWithUnknown(t *testing.T) {
	a := []Advice{{Level: Bad}, {Level: Unknown}}
	if got := WorstLevel(a); got != Unknown {
		t.Fatalf("got %v want Unknown", got)
	}
}

func TestLevelStringUnknown(t *testing.T) {
	if Unknown.String() != "unknown" {
		t.Fatalf("got %q", Unknown.String())
	}
}

func TestLevelJSONRoundTrip(t *testing.T) {
	levels := []Level{OK, Info, Todo, Warn, Bad, Unknown}
	for _, l := range levels {
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatalf("Marshal(%v): %v", l, err)
		}
		var got Level
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("Unmarshal(%s): %v", b, err)
		}
		if got != l {
			t.Errorf("round-trip: got %v want %v", got, l)
		}
	}
}

func TestLevelMarshalWarn(t *testing.T) {
	b, err := json.Marshal(Warn)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(b) != `"warn"` {
		t.Errorf("got %s want %q", b, "warn")
	}
}

func TestLevelUnmarshalUnknownString(t *testing.T) {
	var l Level
	if err := json.Unmarshal([]byte(`"bogus"`), &l); err == nil {
		t.Error("expected error for unknown level string, got nil")
	}
}

func TestParseLevel(t *testing.T) {
	cases := map[string]Level{
		"ok": OK, "info": Info, "todo": Todo, "warn": Warn, "bad": Bad, "unknown": Unknown,
	}
	for s, want := range cases {
		got, err := ParseLevel(s)
		if err != nil {
			t.Errorf("ParseLevel(%q): unexpected error %v", s, err)
		}
		if got != want {
			t.Errorf("ParseLevel(%q) = %v want %v", s, got, want)
		}
	}
	if _, err := ParseLevel("bogus"); err == nil {
		t.Error("ParseLevel(bogus): expected error, got nil")
	}
	if _, err := ParseLevel(""); err == nil {
		t.Error("ParseLevel(\"\"): expected error, got nil")
	}
}

func sampleAdvice() []Advice {
	return []Advice{
		{Level: OK, Title: "ok1"},
		{Level: Info, Title: "info1"},
		{Level: Todo, Title: "todo1"},
		{Level: Warn, Title: "warn1"},
		{Level: Bad, Title: "bad1"},
		{Level: Unknown, Title: "unk1"},
	}
}

func TestFilterByMinLevelOKKeepsAll(t *testing.T) {
	in := sampleAdvice()
	got := FilterByMinLevel(in, OK)
	if len(got) != len(in) {
		t.Fatalf("min=OK kept %d items, want %d (all)", len(got), len(in))
	}
}

func TestFilterByMinLevelWarn(t *testing.T) {
	got := FilterByMinLevel(sampleAdvice(), Warn)
	// Keeps warn, bad, unknown; drops ok, info, todo.
	wantTitles := map[string]bool{"warn1": true, "bad1": true, "unk1": true}
	if len(got) != 3 {
		t.Fatalf("min=Warn kept %d items, want 3: %+v", len(got), got)
	}
	for _, a := range got {
		if !wantTitles[a.Title] {
			t.Errorf("min=Warn kept unexpected item %q", a.Title)
		}
	}
}

func TestFilterByMinLevelBad(t *testing.T) {
	got := FilterByMinLevel(sampleAdvice(), Bad)
	if len(got) != 2 {
		t.Fatalf("min=Bad kept %d items, want 2 (bad+unknown)", len(got))
	}
}

func TestFilterByMinLevelPreservesOrder(t *testing.T) {
	in := []Advice{
		{Level: Bad, Title: "first"},
		{Level: Warn, Title: "second"},
		{Level: Bad, Title: "third"},
	}
	got := FilterByMinLevel(in, Warn)
	if len(got) != 3 || got[0].Title != "first" || got[1].Title != "second" || got[2].Title != "third" {
		t.Fatalf("order not preserved: %+v", got)
	}
}

func TestSortBySeverity(t *testing.T) {
	in := []Advice{
		{Level: OK, Title: "ok1"},
		{Level: Unknown, Title: "unk1"},
		{Level: Info, Title: "info1"},
		{Level: Bad, Title: "bad1"},
		{Level: Warn, Title: "warn1"},
		{Level: Todo, Title: "todo1"},
	}
	got := SortBySeverity(in)
	wantOrder := []Level{Bad, Warn, Todo, Info, OK, Unknown}
	if len(got) != len(wantOrder) {
		t.Fatalf("len mismatch: %d", len(got))
	}
	for i, lv := range wantOrder {
		if got[i].Level != lv {
			t.Errorf("position %d: got %v want %v", i, got[i].Level, lv)
		}
	}
}

func TestSortBySeverityStable(t *testing.T) {
	in := []Advice{
		{Level: Warn, Title: "w-a"},
		{Level: Warn, Title: "w-b"},
		{Level: Warn, Title: "w-c"},
	}
	got := SortBySeverity(in)
	if got[0].Title != "w-a" || got[1].Title != "w-b" || got[2].Title != "w-c" {
		t.Fatalf("stable order broken: %+v", got)
	}
}

func TestSortBySeverityDoesNotMutateInput(t *testing.T) {
	in := []Advice{{Level: OK, Title: "x"}, {Level: Bad, Title: "y"}}
	_ = SortBySeverity(in)
	if in[0].Title != "x" || in[1].Title != "y" {
		t.Fatalf("input was mutated: %+v", in)
	}
}
