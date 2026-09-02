// Package suite asserts things about this repo's own test suite, rather than
// about the product. It is the only package here whose subject is the tests.
package suite

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A test parked on an open issue reports as covered, and the suite goes green
// over a bug nobody is watching. `TestAccAppResource_mode` carried a
// `t.Skip("fogpipe/cloud-workspace#226: ...")` as its first statement, so the
// acceptance suite called the serverless mode switch covered for as long as
// #226 stayed open — it ran, passed that test, and said nothing about a
// provider that could not plan a serverless app at all. Near neighbour of
// ADR-104: there an applied rule reading a series that does not exist is
// healthy, evaluating and permanently inactive; here a test is compiled,
// listed, reported and permanently inert. Both answer every question about
// themselves correctly, and neither can fire.
//
// The discriminator needs no convention anyone has to remember, because the
// tree already draws it: a legitimate skip is CONDITIONAL on something absent
// from the environment — TEST_DATABASE_URL, FPCLOUD_API_KEY, TF_ACC,
// TEST_KUBECONFIG — which is a test declining to run because its inputs are
// not here. A parked one is UNCONDITIONAL: it runs, and skips, every time.
// That is the whole rule, and refusing is the point — the only way to park a
// test becomes deleting it, and git carries it back.
//
// Scope, stated rather than implied (ADR-095): this reaches `t.Skip` and its
// spellings and nothing else. A test can also be parked by an early return, by
// commenting out its body, or by renaming it off the prefix the suite runs, and
// none of those is distinguishable from ordinary code — a check that guessed at
// them would refuse working tests, so this one says plainly that it does not
// look. `fogpipe/cloud-workspace#253`.
func TestNoUnconditionalSkip(t *testing.T) {
	root := repoRoot(t)

	var offenders []string
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
		ast.Walk(&skipWalker{fset: fset, path: rel, out: &offenders}, parsed)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	// Without this the check passes loudest when it is reading nothing at all —
	// a moved root or a broken walk would report a clean suite forever, which is
	// the failure this test exists to name.
	if files == 0 {
		t.Fatalf("no _test.go files found under %s; this check read nothing", root)
	}

	for _, o := range offenders {
		t.Errorf("%s: skips unconditionally, so it reports as covered while never running.\n"+
			"\tGuard the skip on the input it needs (an unset env var, no cluster), or delete the test — "+
			"parking it on an open issue makes the suite green over the bug.", o)
	}
}

type skipWalker struct {
	fset *token.FileSet
	path string
	// Non-zero once inside a statement that can decline to run its body. Loops
	// are deliberately not counted: a `for` guards nothing, and a skip in its
	// body still runs on the first iteration.
	guarded int
	out     *[]string
}

func (w *skipWalker) Visit(n ast.Node) ast.Visitor {
	switch node := n.(type) {
	case *ast.IfStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
		inner := *w
		inner.guarded++
		return &inner
	case *ast.CallExpr:
		sel, ok := node.Fun.(*ast.SelectorExpr)
		if !ok || w.guarded > 0 {
			return w
		}
		switch sel.Sel.Name {
		case "Skip", "Skipf", "SkipNow":
			pos := w.fset.Position(node.Pos())
			*w.out = append(*w.out, w.path+":"+strconv.Itoa(pos.Line))
		}
	}
	return w
}

// The repo this file is in, found by the go.mod above it — the check reads the
// whole tree, not the package it happens to be compiled into.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}
