package carve

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The corpus job must actually RUN every test that needs the corpus.
//
// It did not. corpus_ast_test.go's two AST checks skip unless
// CARVE_SPEC_CORPUS is set, and the only job that sets it ran
// "go test -run TestSpecCorpus", a pattern matching neither. There was no
// configuration under which they executed: unset the variable and they skip,
// set it and the filter excluded them. Both were present, correct, and dead
// (markup-carve/carve-go#29, the class catalogued in markup-carve/carve#755).
//
// Widening the filter fixed it. This is what stops it coming back, and it is
// deliberately not "the job has no -run flag" - a future job may want one. It
// asks the question that matters: is every corpus-gated test in this package
// reachable from the command the corpus job runs?

// A test is corpus-gated when its body reaches the corpus, either through the
// helper or through the environment variable directly. Derived from the source
// rather than listed, so a test added later is covered without editing this one
// - a hand-maintained list would reproduce the original defect one rename later.
func corpusGatedTests(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Fatalf("parsing this package: %v", err)
	}
	var names []string
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			if !strings.HasSuffix(path, "_test.go") {
				continue
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || fn.Body == nil {
					continue
				}
				if !strings.HasPrefix(fn.Name.Name, "Test") {
					continue
				}
				gated := false
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return !gated
					}
					switch fun := call.Fun.(type) {
					case *ast.Ident:
						// One of this package's corpus helpers, each of which
						// reaches corpusDocuments and so inherits its skip.
						switch fun.Name {
						case "corpusDocuments", "corpusTrees", "producedNodeTypes", "schemaDefinitions":
							gated = true
						}
					case *ast.SelectorExpr:
						// os.Getenv("CARVE_SPEC_CORPUS") - the gate itself.
						pkg, ok := fun.X.(*ast.Ident)
						if !ok || pkg.Name != "os" || fun.Sel.Name != "Getenv" {
							return !gated
						}
						if len(call.Args) == 1 {
							if lit, ok := call.Args[0].(*ast.BasicLit); ok &&
								lit.Value == `"CARVE_SPEC_CORPUS"` {
								gated = true
							}
						}
					}
					return !gated
				})
				if gated {
					names = append(names, fn.Name.Name)
				}
			}
		}
	}
	// The ablation: this package has three corpus-gated tests, so a scan that
	// finds fewer is reading nothing rather than reporting a clean result.
	if len(names) < 3 {
		t.Fatalf("found only %d corpus-gated test(s) (%s); this package has three, so the scan is not reading the sources",
			len(names), strings.Join(names, ", "))
	}
	return names
}

// The `go test` invocation of the job that sets CARVE_SPEC_CORPUS.
func corpusJobCommand(t *testing.T) string {
	t.Helper()
	blob, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("reading the workflow: %v", err)
	}
	lines := strings.Split(string(blob), "\n")
	seenVariable := false
	for _, line := range lines {
		if strings.Contains(line, "CARVE_SPEC_CORPUS:") {
			seenVariable = true
			continue
		}
		if !seenVariable {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if command, ok := strings.CutPrefix(trimmed, "run:"); ok {
			return strings.TrimSpace(command)
		}
	}
	if !seenVariable {
		t.Fatal("no job in ci.yml sets CARVE_SPEC_CORPUS, so the corpus tests cannot run at all")
	}
	t.Fatal("the job that sets CARVE_SPEC_CORPUS has no run: command after it")
	return ""
}

func TestTheCorpusJobRunsEveryCorpusGatedTest(t *testing.T) {
	command := corpusJobCommand(t)
	gated := corpusGatedTests(t)

	if !strings.Contains(command, "go test") {
		t.Fatalf("the corpus job's command is %q, which does not run go test", command)
	}

	fields := strings.Fields(command)
	pattern := ""
	for i, field := range fields {
		if field == "-run" && i+1 < len(fields) {
			pattern = strings.Trim(fields[i+1], `"'`)
		}
		if rest, ok := strings.CutPrefix(field, "-run="); ok {
			pattern = strings.Trim(rest, `"'`)
		}
	}
	if pattern == "" {
		// No filter: everything the package defines runs. This is the shape the
		// job has today.
		return
	}

	// Go's -run matches the pattern against the test name unanchored, so this
	// mirrors it rather than requiring a full match.
	filter, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("the corpus job's -run pattern %q does not compile: %v", pattern, err)
	}
	var unreachable []string
	for _, name := range gated {
		if !filter.MatchString(name) {
			unreachable = append(unreachable, name)
		}
	}
	if len(unreachable) > 0 {
		t.Fatalf("the corpus job runs `go test -run %s`, which never executes %s. "+
			"These tests need CARVE_SPEC_CORPUS and only that job sets it, so under this filter "+
			"there is no configuration in which they run (markup-carve/carve-go#29). Widen the "+
			"pattern or drop it.", pattern, strings.Join(unreachable, ", "))
	}

	// The ablation. Without it the loop above passes identically whether the
	// pattern was applied or quietly ignored.
	if regexp.MustCompile("TestSpecCorpus").MatchString("TestEveryRecordedNodeTypeStillReachesTheTree") {
		t.Fatal("the filter check is not matching names")
	}
}
