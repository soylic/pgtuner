package pg

import (
	"reflect"
	"testing"
)

func TestParseConninfo(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]string
	}{
		{"plain", "host=10.0.0.5 port=5432 user=repl", map[string]string{"host": "10.0.0.5", "port": "5432", "user": "repl"}},
		{"quoted", "host='db one' application_name='node 2' password='it\\'s'", map[string]string{"host": "db one", "application_name": "node 2", "password": "it's"}},
		{"extra spaces", "  host=a   port=1  ", map[string]string{"host": "a", "port": "1"}},
		{"empty", "", map[string]string{}},
		{"broken tail keeps earlier keys", "host=a oops", map[string]string{"host": "a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseConninfo(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseConninfo(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
