package patroni

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const sample = `{"scope":"demo","pause":true,"members":[
 {"name":"pg1","role":"leader","state":"running","host":"pg1","port":5432,"timeline":2},
 {"name":"pg2","role":"replica","state":"streaming","host":"pg2","port":5432,"timeline":2,"lag":1024},
 {"name":"pg3","role":"replica","state":"start failed","host":"pg3","port":5432,"timeline":1,"lag":"unknown"}]}`

func TestParse(t *testing.T) {
	c, err := parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if c.Scope != "demo" || !c.Paused || len(c.Members) != 3 {
		t.Fatalf("cluster = %+v", c)
	}
	if c.Members[0].LagBytes != -1 || c.Members[1].LagBytes != 1024 || c.Members[2].LagBytes != -1 {
		t.Errorf("lag = %d %d %d, want -1 1024 -1", c.Members[0].LagBytes, c.Members[1].LagBytes, c.Members[2].LagBytes)
	}
	if c.Members[2].State != "start failed" || c.Members[1].Timeline != 2 {
		t.Errorf("member = %+v", c.Members[2])
	}
}

func TestParseRejectsNonPatroni(t *testing.T) {
	for _, body := range []string{`{}`, `{"members":[]}`, `<html>`, ``} {
		if _, err := parse([]byte(body)); err == nil {
			t.Errorf("parse(%q) accepted a body that is not Patroni", body)
		}
	}
}

func TestBaseURL(t *testing.T) {
	good := map[string]string{
		"http://pg1:8008":             "http://pg1:8008",
		"https://pg1:8008/cluster":    "https://pg1:8008",
		"http://user:pw@pg1:8008/x?a": "http://pg1:8008",
	}
	for in, want := range good {
		if got, err := BaseURL(in); err != nil || got != want {
			t.Errorf("BaseURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "pg1:8008", "ftp://pg1", "file:///etc/passwd", "http://"} {
		if _, err := BaseURL(in); err == nil {
			t.Errorf("BaseURL(%q) should fail", in)
		}
	}
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cluster" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(sample))
	}))
	defer srv.Close()
	c, err := Fetch(context.Background(), srv.URL)
	if err != nil || len(c.Members) != 3 {
		t.Fatalf("Fetch = %+v, %v", c, err)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/", http.StatusFound)
	}))
	defer bad.Close()
	if _, err := Fetch(context.Background(), bad.URL); err == nil {
		t.Error("a redirect must not be followed or accepted")
	}
}
