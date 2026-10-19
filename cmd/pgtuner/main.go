// Command pgtuner analyzes a PostgreSQL server and reports tuning advice.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/soylic/pgtuner/internal/report"
	"github.com/soylic/pgtuner/pkg/pgtuner"
	"github.com/soylic/pgtuner/pkg/tuner"
)

// version defaults to "dev". The release build sets it with -ldflags "-X main.version=<tag>".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable entry point. args excludes the program name.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pgtuner", flag.ContinueOnError)
	fs.SetOutput(stderr)

	dsn := fs.String("dsn", "", "libpq connection string (DSN)")
	host := fs.String("host", "", "PostgreSQL host")
	port := fs.Int("port", 5432, "PostgreSQL port")
	user := fs.String("user", "", "PostgreSQL user")
	dbname := fs.String("dbname", "", "PostgreSQL database name")
	format := fs.String("format", "terminal", "output format: terminal (alias text), json, csv, or markdown (alias md)")
	osInspect := fs.Bool("os-inspect", false, "collect OS-level facts (Linux only)")
	noColor := fs.Bool("no-color", false, "turn off ANSI color in terminal output. This flag has no effect on json, csv, or markdown output")
	showVersion := fs.Bool("version", false, "print the tool version and exit")
	minLevel := fs.String("min-level", "ok", "show only advice at or above this severity (ok/info/todo/warn/bad/unknown). The summary always counts all advice")
	sortBy := fs.String("sort", "section", "advice order: section (group by section) or severity (Bad to Unknown)")
	allDatabases := fs.Bool("all-databases", false, "run per-database checks on every user database. Cluster-wide checks run once")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *showVersion {
		fmt.Fprintln(stdout, version)
		return 0
	}

	// Validate flags before connecting so a typo fails fast.
	min, err := pgtuner.ParseLevel(*minLevel)
	if err != nil {
		fmt.Fprintf(stderr, "pgtuner: -min-level: %v\n", err)
		return 2
	}

	switch *sortBy {
	case "section", "severity":
	default:
		fmt.Fprintf(stderr, "pgtuner: -sort: invalid value %q (use section or severity)\n", *sortBy)
		return 2
	}

	outFormat := strings.ToLower(*format)
	switch outFormat {
	case "text":
		outFormat = "terminal"
	case "md":
		outFormat = "markdown"
	case "terminal", "json", "csv", "markdown":
	default:
		fmt.Fprintf(stderr, "pgtuner: -format: invalid value %q (use terminal, json, csv, or markdown)\n", *format)
		return 2
	}

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

	// The exit code comes from the full advice set, before display filtering.
	exit := rep.ExitCode()

	if *sortBy == "severity" {
		rep.Advice = pgtuner.SortBySeverity(rep.Advice)
	}
	rep.Advice = pgtuner.FilterByMinLevel(rep.Advice, min)

	switch outFormat {
	case "json":
		if err := report.WriteJSON(stdout, rep); err != nil {
			fmt.Fprintf(stderr, "pgtuner: write JSON: %v\n", err)
			return 2
		}
	case "csv":
		if err := report.WriteCSV(stdout, rep); err != nil {
			fmt.Fprintf(stderr, "pgtuner: write CSV: %v\n", err)
			return 2
		}
	case "markdown":
		if err := report.WriteMarkdown(stdout, rep); err != nil {
			fmt.Fprintf(stderr, "pgtuner: write Markdown: %v\n", err)
			return 2
		}
	default: // terminal
		// ColorEnabled needs an *os.File, so other writers (tests) get no color.
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

// buildDSN assembles a postgres:// URL from the flags and omits empty parts.
// The password comes from PG* env or ~/.pgpass. It returns "" when no part is given.
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
