package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestSplitList(t *testing.T) {
	got := splitList(" a, b ,,c ")
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("splitList = %q", got)
	}
	if splitList("") != nil {
		t.Error("empty input must give nil")
	}
}

func TestRunRejectsBadFlagValues(t *testing.T) {
	cases := [][]string{
		{"-fail-on", "severe"},
		{"-color", "rainbow"},
		{"-sort", "random"},
		{"-timeout", "-1s"},
		{"-only", "no_such_check"},
		{"-skip", "no_such_check"},
		{"-ignore", "["},
		{"stray-argument"},
	}
	for _, args := range cases {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 {
			t.Errorf("run(%v) = %d, want 2 (stderr %q)", args, code, errb.String())
		}
	}
}

func TestRunListChecksAndRules(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-list-checks"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "topology\n") || !strings.Contains(out.String(), "autovacuum_settings\n") {
		t.Errorf("-list-checks = %q", out.String())
	}
	out.Reset()
	if code := run([]string{"-list-rules"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "wal.") || !strings.Contains(out.String(), "analysis.database-failed") {
		t.Errorf("-list-rules is missing known IDs")
	}
}

func writeReport(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunDiff(t *testing.T) {
	dir := t.TempDir()
	old := writeReport(t, dir, "old.json", `{"server_version":"17","advice":[{"id":"wal.fsync-off","level":"bad","section":"WAL","title":"fsync off","message":"m"}],"summary":{"bad":1},"grade":"C"}`)
	same := writeReport(t, dir, "same.json", `{"server_version":"17","advice":[{"id":"wal.fsync-off","level":"bad","section":"WAL","title":"fsync off","message":"m"}],"summary":{"bad":1},"grade":"C"}`)
	worse := writeReport(t, dir, "worse.json", `{"server_version":"17","advice":[{"id":"wal.fsync-off","level":"bad","section":"WAL","title":"fsync off","message":"m"},{"id":"connection.ssl-disabled","level":"warn","section":"Connection","title":"SSL disabled","message":"m"}],"summary":{"bad":1,"warn":1},"grade":"D"}`)
	fixed := writeReport(t, dir, "fixed.json", `{"server_version":"17","advice":[],"summary":{},"grade":"A"}`)

	run1 := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := run(append([]string{"-diff"}, args...), &out, &errb)
		return code, out.String(), errb.String()
	}
	if code, out, _ := run1(old, same); code != 0 || !strings.Contains(out, "No findings changed.") {
		t.Errorf("same: code %d, out %q", code, out)
	}
	if code, out, _ := run1(old, fixed); code != 0 || !strings.Contains(out, "Resolved (1)") || !strings.Contains(out, "C -> A") {
		t.Errorf("fixed: code %d, out %q", code, out)
	}
	// A new warn is not a regression at the default -fail-on bad, but it is at warn.
	if code, _, _ := run1(old, worse); code != 0 {
		t.Errorf("new warn at -fail-on bad: code %d, want 0", code)
	}
	if code, out, _ := run1("-fail-on", "warn", old, worse); code != 1 || !strings.Contains(out, "connection.ssl-disabled") {
		t.Errorf("new warn at -fail-on warn: code %d, out %q", code, out)
	}
	if code, _, errs := run1(old); code != 2 || !strings.Contains(errs, "two files") {
		t.Errorf("one file: code %d, stderr %q", code, errs)
	}
	if code, _, _ := run1(old, filepath.Join(dir, "missing.json")); code != 2 {
		t.Errorf("missing file: code %d, want 2", code)
	}
	bad := writeReport(t, dir, "bad.json", "not json")
	if code, _, _ := run1(old, bad); code != 2 {
		t.Errorf("bad json: code %d, want 2", code)
	}
	if code, out, _ := run1("-format", "json", old, fixed); code != 0 || !strings.Contains(out, `"resolved"`) {
		t.Errorf("json diff: code %d, out %q", code, out)
	}
}

func TestBuildDSNSocketAndIPv6(t *testing.T) {
	cases := []struct {
		name               string
		user, host, dbname string
		port               int
		want               string
	}{
		{"socket directory", "postgres", "/var/run/postgresql", "mydb", 5432, "host=/var/run/postgresql port=5432 user=postgres dbname=mydb"},
		{"socket without port", "", "/tmp", "", 0, "host=/tmp"},
		{"socket path with a space", "", "/tmp/my sockets", "", 5432, "host='/tmp/my sockets' port=5432"},
		{"ipv6 host", "alice", "::1", "mydb", 5432, "postgres://alice@[::1]:5432/mydb"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildDSN(c.user, c.host, c.port, c.dbname)
			if got != c.want {
				t.Fatalf("buildDSN = %q, want %q", got, c.want)
			}
			cfg, err := pgx.ParseConfig(got)
			if err != nil {
				t.Fatalf("pgx cannot parse %q: %v", got, err)
			}
			if c.host == "::1" {
				if cfg.Host != "::1" || cfg.Port != 5432 {
					t.Errorf("host %q port %d", cfg.Host, cfg.Port)
				}
			} else if cfg.Host != c.host {
				t.Errorf("host = %q, want %q", cfg.Host, c.host)
			}
		})
	}
}

func TestFlagsMayFollowPositionalArguments(t *testing.T) {
	dir := t.TempDir()
	a := writeReport(t, dir, "a.json", `{"server_version":"17","advice":[],"summary":{},"grade":"A"}`)
	var out, errb bytes.Buffer
	if code := run([]string{"-diff", a, a, "-format", "json"}, &out, &errb); code != 0 || !strings.Contains(out.String(), `"old_grade"`) {
		t.Errorf("flags after files: code %d, out %q, err %q", code, out.String(), errb.String())
	}
}
