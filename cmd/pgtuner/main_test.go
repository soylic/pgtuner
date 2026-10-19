package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code: got %d want 0 (stderr: %s)", code, stderr.String())
	}
	// -version must print the current value of version, which is never empty.
	if version == "" {
		t.Fatal("version must not be empty")
	}
	got := strings.TrimSpace(stdout.String())
	if got != version {
		t.Errorf("version output: got %q want %q", got, version)
	}
	if stderr.Len() != 0 {
		t.Errorf("unexpected stderr: %q", stderr.String())
	}
}

func TestRunFormatBogus(t *testing.T) {
	var stdout, stderr bytes.Buffer
	// An invalid -format must exit with code 2 before any connection attempt.
	code := run([]string{"-format", "bogus"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code: got %d want 2 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "-format") {
		t.Errorf("expected stderr to mention -format, got: %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("unexpected stdout: %q", stdout.String())
	}
}

func TestBuildDSN(t *testing.T) {
	cases := []struct {
		name       string
		user, host string
		port       int
		dbname     string
		want       string
	}{
		{
			name: "all empty returns empty",
			want: "",
		},
		{
			name: "host+port+user+dbname",
			user: "alice", host: "db.example.com", port: 5432, dbname: "mydb",
			want: "postgres://alice@db.example.com:5432/mydb",
		},
		{
			name: "host+port without user",
			host: "db.example.com", port: 5432, dbname: "mydb",
			want: "postgres://db.example.com:5432/mydb",
		},
		{
			name: "non-default port",
			user: "alice", host: "db.example.com", port: 5433, dbname: "mydb",
			want: "postgres://alice@db.example.com:5433/mydb",
		},
		{
			name: "host+port without dbname",
			user: "alice", host: "db.example.com", port: 5432,
			want: "postgres://alice@db.example.com:5432",
		},
		{
			name: "user without host places user as userinfo",
			user: "alice", dbname: "mydb",
			want: "postgres://alice@/mydb",
		},
		{
			name: "port 0 with host omits port",
			host: "db.example.com", port: 0, dbname: "mydb",
			want: "postgres://db.example.com/mydb",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildDSN(tc.user, tc.host, tc.port, tc.dbname)
			if got != tc.want {
				t.Errorf("buildDSN(%q,%q,%d,%q) = %q, want %q",
					tc.user, tc.host, tc.port, tc.dbname, got, tc.want)
			}
			// Verify no ":0" appears in any result.
			if strings.Contains(got, ":0") {
				t.Errorf("buildDSN result contains \":0\": %q", got)
			}
		})
	}
}
