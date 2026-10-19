package checks

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// idPattern is <check>.<condition>: lowercase words, the check part may use underscores.
var idPattern = regexp.MustCompile(`^[a-z0-9_]+\.[a-z0-9]+(-[a-z0-9]+)*$`)

// adviceLiterals returns every pgtuner.Advice composite literal in f, including
// the elided ones inside []pgtuner.Advice{{...}}.
func adviceLiterals(f *ast.File) []*ast.CompositeLit {
	isAdvice := func(e ast.Expr) bool {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Advice" {
			return false
		}
		id, ok := sel.X.(*ast.Ident)
		return ok && id.Name == "pgtuner"
	}
	var out []*ast.CompositeLit
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		switch {
		case isAdvice(lit.Type):
			out = append(out, lit)
		default:
			if at, ok := lit.Type.(*ast.ArrayType); ok && isAdvice(at.Elt) {
				for _, el := range lit.Elts {
					if inner, ok := el.(*ast.CompositeLit); ok && inner.Type == nil {
						out = append(out, inner)
					}
				}
			}
		}
		return true
	})
	return out
}

// literalIDs maps each ID found in the package sources to the places that use it.
func literalIDs(t *testing.T) (ids map[string][]string, problems []string) {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	ids = map[string][]string{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, lit := range adviceLiterals(f) {
			pos := fset.Position(lit.Pos()).String()
			var idExpr ast.Expr
			for _, el := range lit.Elts {
				if kv, ok := el.(*ast.KeyValueExpr); ok {
					if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "ID" {
						idExpr = kv.Value
					}
				}
			}
			bl, ok := idExpr.(*ast.BasicLit)
			if !ok || bl.Kind != token.STRING {
				problems = append(problems, pos+": Advice needs ID: with a string literal")
				continue
			}
			id, _ := strconv.Unquote(bl.Value)
			if !idPattern.MatchString(id) {
				problems = append(problems, pos+": bad ID "+strconv.Quote(id)+" (want <check>.<condition-in-kebab-case>)")
			}
			ids[id] = append(ids[id], pos)
		}
	}
	return ids, problems
}

// Every advice in the checks package has a constant, well-formed ID, and no ID is used at two places.
func TestAdviceIDsAreStableAndUnique(t *testing.T) {
	ids, problems := literalIDs(t)
	for id, where := range ids {
		if len(where) > 1 {
			problems = append(problems, "ID "+strconv.Quote(id)+" used at several places: "+strings.Join(where, ", "))
		}
	}
	if len(problems) > 0 {
		t.Fatalf("%d problem(s):\n%s", len(problems), strings.Join(problems, "\n"))
	}
	if len(ids) < 100 {
		t.Errorf("found only %d IDs, the scan looks wrong", len(ids))
	}
}

// rules_gen.go must list exactly the IDs in the source. Run go generate ./internal/checks to refresh it.
func TestGeneratedRuleIDsAreCurrent(t *testing.T) {
	ids, _ := literalIDs(t)
	want := make([]string, 0, len(ids))
	for id := range ids {
		want = append(want, id)
	}
	sort.Strings(want)
	if strings.Join(want, "\n") != strings.Join(generatedRuleIDs, "\n") {
		t.Fatal("rules_gen.go is out of date: run go generate ./internal/checks")
	}
}
