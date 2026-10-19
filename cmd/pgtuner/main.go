// Command pgtuner analyzes a PostgreSQL server and reports tuning advice.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/soylic/pgtuner/internal/checks"
	"github.com/soylic/pgtuner/internal/pg"
	"github.com/soylic/pgtuner/internal/report"
	"github.com/soylic/pgtuner/pkg/pgtuner"
	"github.com/soylic/pgtuner/pkg/tuner"
)

// version defaults to "dev". The release build sets it with -ldflags "-X main.version=<semver>".
// Read it through resolveVersion, which also covers "go install pkg@v1.2.3".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable entry point. args excludes the program name.
func run(args []string, stdout, stderr io.Writer) int {
	info := readBuildInfo()
	ver := resolveVersion(version, info)
	fs := flag.NewFlagSet("pgtuner", flag.ContinueOnError)
	fs.SetOutput(stderr)

	dsn := fs.String("dsn", "", "libpq connection string (DSN)")
	host := fs.String("host", "", "PostgreSQL host")
	port := fs.Int("port", 5432, "PostgreSQL port")
	user := fs.String("user", "", "PostgreSQL user")
	dbname := fs.String("dbname", "", "PostgreSQL database name")
	format := fs.String("format", "terminal", "output format: terminal (alias text), json, csv, or markdown (alias md)")
	osInspect := fs.Bool("os-inspect", false, "collect OS-level facts (Linux only)")
	colorMode := fs.String("color", "auto", "ANSI color in terminal output: auto (only on a terminal), always, or never")
	noColor := fs.Bool("no-color", false, "same as -color never")
	showVersion := fs.Bool("version", false, "print the tool version and exit")
	minLevel := fs.String("min-level", "ok", "show only advice at or above this severity (ok/info/todo/warn/bad/unknown). The summary always counts all advice")
	sortBy := fs.String("sort", "auto", "advice order: auto (severity on a terminal, section elsewhere), section, or severity")
	allDatabases := fs.Bool("all-databases", false, "run per-database checks on every user database. Cluster-wide checks run once")
	only := fs.String("only", "", "run only these checks (comma-separated names, see -list-checks)")
	skip := fs.String("skip", "", "do not run these checks (comma-separated names, see -list-checks)")
	ignore := fs.String("ignore", "", "drop findings by ID (comma-separated, * allowed, see -list-rules). Ignored findings do not count in the summary or the grade")
	failOn := fs.String("fail-on", "bad", "exit with code 1 when a finding is at this level or worse: none, info, todo, warn, or bad")
	timeout := fs.Duration("timeout", pg.DefaultStatementTimeout, "time limit for each query. 0 turns the limit off")
	patroniURL := fs.String("patroni-url", "", "Patroni REST API, such as http://pg1:8008. Gives the member list and a certain match")
	showIDs := fs.Bool("ids", false, "show each finding's ID in terminal output, for -ignore")
	diff := fs.Bool("diff", false, "compare two JSON reports: pgtuner -diff old.json new.json")
	listChecks := fs.Bool("list-checks", false, "print the check names and exit")
	listRules := fs.Bool("list-rules", false, "print the finding IDs that -ignore accepts and exit")

	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}

	if *showVersion {
		fmt.Fprintln(stdout, describeVersion(ver, info))
		return 0
	}
	if *listChecks {
		fmt.Fprintln(stdout, strings.Join(checks.Default().Names(), "\n"))
		return 0
	}
	if *listRules {
		fmt.Fprintln(stdout, strings.Join(checks.RuleIDs(), "\n"))
		return 0
	}

	// Validate flags before connecting so a typo fails fast.
	min, err := pgtuner.ParseLevel(*minLevel)
	if err != nil {
		fmt.Fprintf(stderr, "pgtuner: -min-level: %v\n", err)
		return 2
	}
	failMin, failEnabled, err := pgtuner.ParseFailOn(*failOn)
	if err != nil {
		fmt.Fprintf(stderr, "pgtuner: -fail-on: %v\n", err)
		return 2
	}
	switch *sortBy {
	case "auto", "section", "severity":
	default:
		fmt.Fprintf(stderr, "pgtuner: -sort: invalid value %q (use auto, section or severity)\n", *sortBy)
		return 2
	}
	if *noColor {
		*colorMode = "never"
	}
	switch *colorMode {
	case "auto", "always", "never":
	default:
		fmt.Fprintf(stderr, "pgtuner: -color: invalid value %q (use auto, always or never)\n", *colorMode)
		return 2
	}
	if *timeout < 0 {
		fmt.Fprintln(stderr, "pgtuner: -timeout: must not be negative")
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

	color := false
	switch *colorMode {
	case "always":
		color = true
	case "auto":
		// ColorEnabled needs an *os.File, so other writers (tests) get no color.
		if f, ok := stdout.(*os.File); ok {
			color = report.ColorEnabled(f)
		}
	}

	if *diff {
		return runDiff(positional, outFormat, color, failMin, failEnabled, stdout, stderr)
	}
	if len(positional) > 0 {
		fmt.Fprintf(stderr, "pgtuner: unexpected argument %q\n", positional[0])
		return 2
	}

	effectiveDSN := *dsn
	if effectiveDSN == "" && (*host != "" || *user != "" || *dbname != "") {
		effectiveDSN = buildDSN(*user, *host, *port, *dbname)
	}

	statementTimeout := *timeout
	if statementTimeout == 0 {
		statementTimeout = -1
	}
	ctx := context.Background()
	rep, err := tuner.Analyze(ctx, pgtuner.Options{
		DSN:              effectiveDSN,
		OSInspect:        *osInspect,
		AllDatabases:     *allDatabases,
		Only:             splitList(*only),
		Skip:             splitList(*skip),
		Ignore:           splitList(*ignore),
		StatementTimeout: statementTimeout,
		PatroniURL:       *patroniURL,
	})
	if err != nil {
		fmt.Fprintf(stderr, "pgtuner: %v\n", err)
		return 2
	}

	// The exit code comes from the full advice set, before display filtering.
	exit := 0
	if failEnabled {
		exit = rep.ExitCodeAt(failMin)
	}

	severity := *sortBy == "severity" || (*sortBy == "auto" && outFormat == "terminal")
	if severity {
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
		if err := report.WriteTerminalOptions(stdout, rep, report.TerminalOptions{Color: color, SeveritySort: severity, Version: ver, ShowIDs: *showIDs}); err != nil {
			fmt.Fprintf(stderr, "pgtuner: write terminal: %v\n", err)
			return 2
		}
	}

	return exit
}

// parseInterspersed parses flags that may come after positional arguments, as in
// "pgtuner -diff old.json new.json -no-color". The flag package stops at the
// first positional argument, so it parses again after taking each one.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return positional, nil
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}

// splitList splits a comma-separated flag value, trimming spaces and dropping empty items.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// runDiff compares two JSON reports. It exits 1 when the new report has a finding
// that is new or worse at failMin or above.
func runDiff(paths []string, format string, color bool, failMin pgtuner.Level, failEnabled bool, stdout, stderr io.Writer) int {
	if len(paths) != 2 {
		fmt.Fprintln(stderr, "pgtuner: -diff needs two files: pgtuner -diff old.json new.json")
		return 2
	}
	if format != "terminal" && format != "json" {
		fmt.Fprintf(stderr, "pgtuner: -diff supports -format terminal and json, not %s\n", format)
		return 2
	}
	var reports [2]pgtuner.Report
	for i, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintf(stderr, "pgtuner: %v\n", err)
			return 2
		}
		if err := json.Unmarshal(b, &reports[i]); err != nil {
			fmt.Fprintf(stderr, "pgtuner: %s is not a pgtuner JSON report: %v\n", p, err)
			return 2
		}
	}
	d := pgtuner.Compare(&reports[0], &reports[1])
	var err error
	if format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		err = enc.Encode(d)
	} else {
		err = report.WriteDiff(stdout, d, color)
	}
	if err != nil {
		fmt.Fprintf(stderr, "pgtuner: write diff: %v\n", err)
		return 2
	}
	if failEnabled && d.Regressed(failMin) {
		return 1
	}
	return 0
}

// buildDSN assembles a connection string from the flags and omits empty parts.
// The password comes from PG* env or ~/.pgpass. It returns "" when no part is given.
// A host that starts with / is a Unix socket directory. A URL cannot hold it
// (pgx rejects the escaped path), so that case uses the key=value form.
func buildDSN(user, host string, port int, dbname string) string {
	if user == "" && host == "" && dbname == "" {
		return ""
	}
	if strings.HasPrefix(host, "/") {
		return keywordDSN(user, host, port, dbname)
	}
	u := url.URL{Scheme: "postgres"}
	if user != "" {
		u.User = url.User(user)
	}
	switch {
	case host != "" && port != 0:
		u.Host = net.JoinHostPort(host, strconv.Itoa(port)) // brackets an IPv6 address
	case host != "":
		u.Host = host
	}
	if dbname != "" {
		u.Path = "/" + dbname
	}
	return u.String()
}

// keywordDSN builds a libpq key=value string.
// src: https://www.postgresql.org/docs/current/libpq-connect.html#LIBPQ-CONNSTRING-KEYWORD-VALUE
func keywordDSN(user, host string, port int, dbname string) string {
	var parts []string
	add := func(k, v string) {
		if v != "" {
			parts = append(parts, k+"="+quoteDSNValue(v))
		}
	}
	add("host", host)
	if port != 0 {
		add("port", strconv.Itoa(port))
	}
	add("user", user)
	add("dbname", dbname)
	return strings.Join(parts, " ")
}

// quoteDSNValue single-quotes a value that holds a space, a quote or a backslash.
func quoteDSNValue(v string) string {
	if !strings.ContainsAny(v, " '\\") {
		return v
	}
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(v) + "'"
}
