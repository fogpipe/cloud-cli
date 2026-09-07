package suite

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestNoUnconditionalSkip refuses a skip that runs every time. This one refuses
// the same thing wearing a disguise: a skip CONDITIONAL on an environment
// variable that nothing in this repository ever sets.
//
// `TestFederationExchange_EndToEnd` is the case it was written for. ADR-021 says
// the token exchange "must be verified against a live API + DB — not shipped
// from static review alone", and that test does exactly that. It was gated on
// `FPCLOUD_TEST_DB`, a name nothing in the repository, nothing in CI, and not
// even its own package's TestMain has ever set — every other db-backed test in
// the tree reads TEST_DATABASE_URL. So the only end-to-end assertion about a
// path ADR-021 calls security-critical skipped on every run, in every
// environment, from the day it was written; it then rotted unobserved, and its
// first ever execution failed against a table the code had stopped using
// (fogpipe/cloud-workspace#386).
//
// It is strictly harder to see than the unconditional case. An unconditional
// skip at least looks unusual; this one is indistinguishable from the sixty
// legitimate skips beside it, because it IS the legitimate shape — a test
// declining because its inputs are not here. The only thing separating them is
// whether the input can ever arrive, and that is a fact about the repository
// rather than about the test, which is why no reading of the test file can
// settle it and why this check exists at all.
//
// ADR-127's own second example is the other half of the argument. Both webhook
// acceptance tests skipped unconditionally with a message naming
// FPCLOUD_ACC_WEBHOOK_REPO, an input they never read; #253 fixed the read, so
// the variable became real — and nothing has ever set it, so the tests still do
// not run anywhere. Fixing the disguise left the emptiness, which is precisely
// what an unconditional-only rule cannot see.
//
// A provider is something that supplies a VALUE: a workflow, a justfile recipe,
// a Makefile, a compose file, or a t.Setenv in this repo's own Go. Comments are
// stripped before matching, because a workflow explaining why a test self-skips
// is the disguise again one layer out. Documentation is deliberately not a
// provider for the same reason: ADR-127's webhook skip documented an escape
// hatch it did not have.
//
// Scope, stated rather than implied (ADR-095): this resolves an env name
// through a direct os.Getenv/os.LookupEnv in the guard and through a local
// variable assigned from one in the same function. A name reached by any other
// route — a package-level helper returning a string, a struct field, a
// constant indirection — is not seen, and this check says so rather than
// implying a reach it lacks. What it does see is every guarded skip in all
// three repos today.
func TestSkipInputsAreProvided(t *testing.T) {
	root := repoRoot(t)

	needed := map[string][]string{} // env name -> where it is required
	files := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "testdata":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		files++

		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			for name, line := range guardedSkipInputs(fn) {
				needed[name] = append(needed[name], rel+":"+strconv.Itoa(fset.Position(line).Line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	// Same reason as the sibling check: reading nothing must not report clean.
	if files == 0 {
		t.Fatalf("no _test.go files found under %s; this check read nothing", root)
	}

	provided := providedNames(t, root)

	var missing []string
	for name := range needed {
		if !provided[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)

	for _, name := range missing {
		sites := needed[name]
		sort.Strings(sites)
		t.Errorf("%s guards a skip at %s, and nothing in this repository ever sets it — "+
			"so those tests skip in every environment, forever, while reporting as covered.\n"+
			"\tGive it a provider that supplies a value (a CI workflow, a justfile recipe, a compose file), "+
			"or delete the tests — a name nobody provides is an unconditional skip in disguise.",
			name, strings.Join(sites, ", "))
	}
}

// guardedSkipInputs returns the env names whose absence can make fn skip,
// mapped to the position of the skip they guard.
func guardedSkipInputs(fn *ast.FuncDecl) map[string]token.Pos {
	// Locals assigned from os.Getenv / os.LookupEnv, so `v := os.Getenv("X"); if
	// v == "" { t.Skip(...) }` resolves — the shape the provider's acceptance
	// helpers use, where the read and the guard are separate statements.
	fromEnv := map[string]string{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, rhs := range as.Rhs {
			name, ok := envNameOf(rhs)
			if !ok || i >= len(as.Lhs) {
				continue
			}
			if id, ok := as.Lhs[i].(*ast.Ident); ok {
				fromEnv[id.Name] = name
			}
		}
		return true
	})

	out := map[string]token.Pos{}
	var walk func(n ast.Node, guards []*ast.IfStmt)
	walk = func(n ast.Node, guards []*ast.IfStmt) {
		if n == nil {
			return
		}
		switch node := n.(type) {
		case *ast.IfStmt:
			inner := append(append([]*ast.IfStmt{}, guards...), node)
			walk(node.Body, inner)
			// An else branch is guarded by the same condition inverted; it is
			// still conditional, and the names it depends on are the same.
			if node.Else != nil {
				walk(node.Else, inner)
			}
			return
		case *ast.CallExpr:
			if sel, ok := node.Fun.(*ast.SelectorExpr); ok {
				switch sel.Sel.Name {
				case "Skip", "Skipf", "SkipNow":
					for _, g := range guards {
						for _, name := range envNamesIn(g, fromEnv) {
							if _, seen := out[name]; !seen {
								out[name] = node.Pos()
							}
						}
					}
				}
			}
		}
		for _, child := range children(n) {
			walk(child, guards)
		}
	}
	walk(fn.Body, nil)
	return out
}

func children(n ast.Node) []ast.Node {
	var out []ast.Node
	ast.Inspect(n, func(c ast.Node) bool {
		if c == nil || c == n {
			return c == n
		}
		out = append(out, c)
		return false
	})
	return out
}

// envNamesIn reads the env names an if-statement's own condition and init
// depend on, directly or through a local assigned from the environment.
func envNamesIn(g *ast.IfStmt, fromEnv map[string]string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for _, n := range []ast.Node{g.Cond, g.Init} {
		if n == nil {
			continue
		}
		ast.Inspect(n, func(c ast.Node) bool {
			switch v := c.(type) {
			case *ast.CallExpr:
				if name, ok := envNameOf(v); ok {
					add(name)
				}
			case *ast.Ident:
				add(fromEnv[v.Name])
			}
			return true
		})
	}
	return out
}

// envNameOf reads the literal name out of os.Getenv("X") / os.LookupEnv("X").
func envNameOf(e ast.Expr) (string, bool) {
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "os" {
		return "", false
	}
	if sel.Sel.Name != "Getenv" && sel.Sel.Name != "LookupEnv" {
		return "", false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	name, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return name, true
}

var setenvCall = regexp.MustCompile(`Setenv\("([A-Za-z_][A-Za-z_0-9]*)"`)

// providedNames is every env name this repository supplies a value for. HOME
// and the like are provided by the operating system rather than by us, so the
// set is seeded with what a process always has.
func providedNames(t *testing.T, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{"HOME": true, "PATH": true, "TMPDIR": true, "USER": true}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor":
				return fs.SkipDir
			}
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if strings.HasSuffix(d.Name(), ".go") {
			for _, m := range setenvCall.FindAllStringSubmatch(string(body), -1) {
				out[m[1]] = true
			}
			return nil
		}
		if !providerFile(rel, d.Name()) {
			return nil
		}
		for _, name := range assignedNames(string(body)) {
			out[name] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading providers under %s: %v", root, err)
	}
	return out
}

// providerFile says whether a file can supply a value to a test process.
func providerFile(rel, name string) bool {
	if strings.HasPrefix(rel, ".github"+string(filepath.Separator)) {
		return strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")
	}
	switch {
	case name == "justfile" || name == "Justfile" || name == "Makefile":
		return true
	case strings.HasPrefix(name, "docker-compose") || strings.HasPrefix(name, "compose."):
		return true
	}
	return false
}

// NAME: value, NAME=value, NAME := value — one shape per provider syntax.
// Matched anywhere on the line rather than only at its start, because the
// commonest provider here is a shell prefix (`TF_ACC=1 NAME=x go test ...`) and
// requiring column zero would refuse it. `==` is excluded so a comparison is
// not read as a provision, and comments are stripped first: a workflow
// explaining why a test self-skips must not be mistaken for one that runs it.
var assignment = regexp.MustCompile(`(?:^|\s)(?:export\s+)?([A-Z_][A-Z_0-9]*)\s*(?::=|=[^=]|:\s)`)

func assignedNames(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		for _, m := range assignment.FindAllStringSubmatch(line, -1) {
			out = append(out, m[1])
		}
	}
	return out
}
