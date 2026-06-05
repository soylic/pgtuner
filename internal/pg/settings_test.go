package pg

import "testing"

func TestSettingsTyped(t *testing.T) {
	s := &Settings{raw: map[string]string{
		"shared_buffers": "16384",
		"fsync":          "on",
		"work_mem":       "4096",
	}}
	if v, _ := s.Int("shared_buffers"); v != 16384 {
		t.Fatalf("int got %d", v)
	}
	if !s.Bool("fsync") {
		t.Fatal("bool got false")
	}
	if _, ok := s.Get("does_not_exist"); ok {
		t.Fatal("missing key reported present")
	}

	// Float: missing key → (0, false)
	if v, ok := s.Float("does_not_exist"); ok || v != 0 {
		t.Fatal("missing key should return (0, false) for Float")
	}

	s2 := &Settings{raw: map[string]string{"completion_target": "0.9", "bad": "notanumber"}}
	if f, ok := s2.Float("completion_target"); !ok || f != 0.9 {
		t.Fatalf("Float got (%v, %v)", f, ok)
	}
	if _, ok := s2.Float("bad"); ok {
		t.Fatal("non-numeric should return ok=false for Float")
	}
}
