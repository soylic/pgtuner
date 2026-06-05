// Command pgtuner analyzes a PostgreSQL server and reports tuning advice.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"

	"github.com/OpenRemedy/pgtuner/internal/report"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
	"github.com/OpenRemedy/pgtuner/pkg/tuner"
)

const version = "0.1.0-dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable entry point. args are the CLI arguments (excluding the
// program name). stdout and stderr are the output writers. Returns an exit code.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pgtuner", flag.ContinueOnError)
	fs.SetOutput(stderr)

	dsn := fs.String("dsn", "", "libpq connection string (DSN)")
	host := fs.String("host", "", "PostgreSQL host")
	port := fs.Int("port", 5432, "PostgreSQL port")
	user := fs.String("user", "", "PostgreSQL user")
	dbname := fs.String("dbname", "", "PostgreSQL database name")
	jsonOut := fs.Bool("json", false, "output report as JSON")
	osInspect := fs.Bool("os-inspect", false, "gather OS-level facts (Linux only)")
	noColor := fs.Bool("no-color", false, "disable ANSI color in terminal output")
	showVersion := fs.Bool("version", false, "print tool version and exit")
	minLevel := fs.String("min-level", "ok", "only show advice at or above this severity (ok/info/todo/warn/bad/unknown); summary stays full")
	sortBy := fs.String("sort", "section", "advice ordering: section (group by section) or severity (Bad→…→Unknown)")
	allDatabases := fs.Bool("all-databases", false, "run per-database checks against every user database (cluster-wide checks run once)")

	if err := fs.Parse(args); err != nil {
		// flag.ContinueOnError already printed usage to stderr.
		return 2
	}

	if *showVersion {
		fmt.Fprintln(stdout, version)
		return 0
	}

	// Validate -min-level early so a typo fails before connecting.
	min, err := pgtuner.ParseLevel(*minLevel)
	if err != nil {
		fmt.Fprintf(stderr, "pgtuner: -min-level: %v\n", err)
		return 2
	}

	// Validate -sort early.
	switch *sortBy {
	case "section", "severity":
	default:
		fmt.Fprintf(stderr, "pgtuner: -sort: invalid value %q (want section or severity)\n", *sortBy)
		return 2
	}

	// Assemble DSN from parts when -dsn not given but components are provided.
	effectiveDSN := *dsn
	if effectiveDSN == "" && (*host != "" || *user != "" || *dbname != "") {
		effectiveDSN = buildDSN(*user, *host, *port, *dbname)
	}

	ctx := context.Background()
	rep, err := tuner.Analyze(ctx, pgtuner.Options{
		DSN:          effectiveDSN,
		OSInspect:    *osInspect,
		AllDatabases: *allDatabases,
	})
	if err != nil {
		fmt.Fprintf(stderr, "pgtuner: %v\n", err)
		return 2
	}

	// Compute exit code from the FULL advice set before any display filtering.
	exit := rep.ExitCode()

	// Apply severity sort (display ordering) before filtering. The Summary on
	// the report is untouched by these display transforms.
	if *sortBy == "severity" {
		rep.Advice = pgtuner.SortBySeverity(rep.Advice)
	}
	// Apply min-level display filter. Summary stays full-cluster truth.
	rep.Advice = pgtuner.FilterByMinLevel(rep.Advice, min)

	if *jsonOut {
		if err := report.WriteJSON(stdout, rep); err != nil {
			fmt.Fprintf(stderr, "pgtuner: write JSON: %v\n", err)
			return 2
		}
	} else {
		// ColorEnabled only works with *os.File; fall back to no-color when
		// stdout is not a real file (e.g. in tests).
		color := false
		if !*noColor {
			if f, ok := stdout.(*os.File); ok {
				color = report.ColorEnabled(f)
			}
		}
		if err := report.WriteTerminal(stdout, rep, color, *sortBy == "severity"); err != nil {
			fmt.Fprintf(stderr, "pgtuner: write terminal: %v\n", err)
			return 2
		}
	}

	return exit
}

// buildDSN assembles a postgres:// URL from individual flags. Empty parts are
// omitted; the password is left to PG* env / ~/.pgpass. Returns "" when no
// parts are given (pgx then uses env/.pgpass entirely).
func buildDSN(user, host string, port int, dbname string) string {
	if user == "" && host == "" && dbname == "" {
		return ""
	}
	u := url.URL{Scheme: "postgres"}
	if user != "" {
		u.User = url.User(user)
	}
	switch {
	case host != "" && port != 0:
		u.Host = fmt.Sprintf("%s:%d", host, port)
	case host != "":
		u.Host = host
	}
	if dbname != "" {
		u.Path = "/" + dbname
	}
	return u.String()
}
