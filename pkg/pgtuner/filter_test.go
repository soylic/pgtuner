package pgtuner

import "testing"

func TestMatchID(t *testing.T) {
	tests := []struct {
		pattern, id string
		want        bool
	}{
		{"connection.ssl-disabled", "connection.ssl-disabled", true},
		{"connection.*", "connection.ssl-disabled", true},
		{"connection.*", "wal.fsync-off", false},
		{"*.query-failed", "schema.index-query-failed", false},
		{"*.index-query-failed", "schema.index-query-failed", true},
		{"autovacuum_settings.*", "autovacuum.dead-tuples", false},
		{"[", "x", false},
	}
	for _, tt := range tests {
		if got := MatchID(tt.pattern, tt.id); got != tt.want {
			t.Errorf("MatchID(%q, %q) = %v, want %v", tt.pattern, tt.id, got, tt.want)
		}
	}
}

func TestValidateIDPattern(t *testing.T) {
	if err := ValidateIDPattern("wal.*"); err != nil {
		t.Errorf("valid pattern rejected: %v", err)
	}
	if err := ValidateIDPattern("["); err == nil {
		t.Error("broken pattern accepted")
	}
}

func TestFilterIgnored(t *testing.T) {
	in := []Advice{
		{ID: "wal.fsync-off", Level: Bad},
		{ID: "connection.ssl-disabled", Level: Warn},
		{ID: "connection.max-connections-high", Level: Info},
		{Level: Info, Title: "no id"},
	}
	got, n := FilterIgnored(in, []string{"connection.*", "wal.nothing"})
	if n != 2 || len(got) != 2 || got[0].ID != "wal.fsync-off" || got[1].Title != "no id" {
		t.Errorf("FilterIgnored = %v (dropped %d)", got, n)
	}
	same, n := FilterIgnored(in, nil)
	if n != 0 || len(same) != len(in) {
		t.Errorf("no patterns must keep everything, got %d kept, %d dropped", len(same), n)
	}
}

func TestParseFailOnAndExitCodeAt(t *testing.T) {
	if _, on, err := ParseFailOn("none"); err != nil || on {
		t.Errorf("none = on %v err %v", on, err)
	}
	if _, _, err := ParseFailOn("unknown"); err == nil {
		t.Error("unknown must be rejected")
	}
	r := NewReport("17", []Advice{{Level: Info}, {Level: Warn}, {Level: Unknown}})
	for _, tt := range []struct {
		min  string
		want int
	}{{"info", 1}, {"warn", 1}, {"bad", 0}} {
		l, _, _ := ParseFailOn(tt.min)
		if got := r.ExitCodeAt(l); got != tt.want {
			t.Errorf("ExitCodeAt(%s) = %d, want %d", tt.min, got, tt.want)
		}
	}
	onlyUnknown := NewReport("17", []Advice{{Level: Unknown}, {Level: OK}})
	if onlyUnknown.ExitCodeAt(Info) != 0 {
		t.Error("Unknown and OK must not fail a run")
	}
}
