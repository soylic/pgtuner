package pg

import "testing"

func TestParseServerVersion(t *testing.T) {
	cases := map[string]ServerVersion{
		"170004": {Major: 17, Minor: 4},
		"160013": {Major: 16, Minor: 13},
		"150000": {Major: 15, Minor: 0},
	}
	for num, want := range cases {
		got, err := ParseServerVersionNum(num)
		if err != nil || got != want {
			t.Fatalf("%s: got %+v err %v", num, got, err)
		}
	}

	for _, bad := range []string{"-1", "0"} {
		if _, err := ParseServerVersionNum(bad); err == nil {
			t.Fatalf("ParseServerVersionNum(%q): expected error, got nil", bad)
		}
	}
}

func TestAtLeast(t *testing.T) {
	v := ServerVersion{Major: 17, Minor: 4}
	if !v.AtLeast(15, 0) || v.AtLeast(18, 0) {
		t.Fatal("AtLeast major comparison wrong")
	}
	if !v.AtLeast(17, 4) {
		t.Fatal("exact match should be true")
	}
	if v.AtLeast(17, 5) {
		t.Fatal("same major, higher minor should be false")
	}
}
