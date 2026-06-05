package pg

import (
	"context"
	"strconv"
)

// Settings is a cached snapshot of pg_settings (name -> setting value).
type Settings struct{ raw map[string]string }

// LoadSettings reads the full pg_settings catalog into memory.
func LoadSettings(ctx context.Context, c *Conn) (*Settings, error) {
	rows, err := c.Query(ctx, "SELECT name, setting FROM pg_settings")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string]string, 350)
	for rows.Next() {
		var n, v string
		if err := rows.Scan(&n, &v); err != nil {
			return nil, err
		}
		m[n] = v
	}
	return &Settings{raw: m}, rows.Err()
}

// Get returns the raw setting value and whether it was present.
func (s *Settings) Get(name string) (string, bool) { v, ok := s.raw[name]; return v, ok }

// Int parses the setting as an int64; ok is false if absent or non-numeric.
func (s *Settings) Int(name string) (int64, bool) {
	v, ok := s.raw[name]
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	return n, err == nil
}

// Bool reports whether the setting equals "on". An absent key returns false;
// for settings whose absence is meaningful, use Get and check the ok value.
func (s *Settings) Bool(name string) bool { return s.raw[name] == "on" }

// Float parses the setting as a float64; ok is false if absent or non-numeric.
func (s *Settings) Float(name string) (float64, bool) {
	v, ok := s.raw[name]
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	return f, err == nil
}

// NewSettings builds a Settings snapshot from a name->value map. Intended for
// tests and for callers that already hold pg_settings values.
func NewSettings(raw map[string]string) *Settings { return &Settings{raw: raw} }
