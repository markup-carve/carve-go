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
						case "corpusDocuments", "corpusTrees", "producedNodeTypes", "schemaDefinitions",
							"requireWholeCorpus", "declaredCorpusSize":
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
	// The ablation, so a scan that read nothing cannot report a clean result.
	// It used to be `len(names) < 3` with "this package has three" written
	// beside it - correct on the day, and a fourth corpus-gated test would have
	// left it correct and useless. Anchoring on a test whose corpus dependence
	// is definitional says the same thing without a number to maintain:
	// TestSpecCorpus reads CARVE_SPEC_CORPUS in its first statement, so a scan
	// that misses it is not reading the sources.
	found := false
	for _, name := range names {
		if name == "TestSpecCorpus" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the scan found %d corpus-gated test(s) (%s) and TestSpecCorpus was not among them; "+
			"that test reads CARVE_SPEC_CORPUS directly, so the scan is not reading the sources",
			len(names), strings.Join(names, ", "))
	}
	return names
}

// Every `go test` invocation that runs with CARVE_SPEC_CORPUS set.
//
// ALL of them, not the first. There are two now - `corpus` measures against the
// spec commit the embedded engine pins and gates on it, `corpus-drift` measures
// against spec main and only reports - and a scan that stopped at the first
// occurrence would leave the second free to carry exactly the narrow -run
// filter this guard exists to forbid. A guard that checks one of two callers is
// the same defect it was written to close, one job later.
func corpusJobCommands(t *testing.T) []string {
	t.Helper()
	blob, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("reading the workflow: %v", err)
	}
	lines := strings.Split(string(blob), "\n")
	var commands []string
	for i, line := range lines {
		if !strings.Contains(line, "CARVE_SPEC_CORPUS:") {
			continue
		}
		command, ok := runCommandAt(lines, i+1)
		if !ok {
			t.Fatalf("the step setting CARVE_SPEC_CORPUS at ci.yml:%d has no run: command after it", i+1)
		}
		commands = append(commands, command)
	}
	if len(commands) == 0 {
		t.Fatal("no job in ci.yml sets CARVE_SPEC_CORPUS, so the corpus tests cannot run at all")
	}
	return commands
}

// runCommandAt returns the next run: value at or after start, joining a block
// scalar (`run: |`) into one string so a multi-line step reads the same as a
// one-line one. Without this, `run: |` would be read as the command "|", and
// the go-test assertion below would fail on a step that does run go test - a
// false alarm that would very likely be silenced by dropping the step from the
// scan, which is how a guard loses its second caller.
func runCommandAt(lines []string, start int) (string, bool) {
	for i := start; i < len(lines); i++ {
		rest, ok := strings.CutPrefix(strings.TrimSpace(lines[i]), "run:")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		switch rest {
		case "|", "|-", ">", ">-":
		default:
			return rest, true
		}
		indent := len(lines[i]) - len(strings.TrimLeft(lines[i], " "))
		var body []string
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "" {
				continue
			}
			if len(lines[j])-len(strings.TrimLeft(lines[j], " ")) <= indent {
				break
			}
			body = append(body, strings.TrimSpace(lines[j]))
		}
		return strings.Join(body, "\n"), true
	}
	return "", false
}

func TestTheCorpusJobRunsEveryCorpusGatedTest(t *testing.T) {
	commands := corpusJobCommands(t)
	gated := corpusGatedTests(t)

	for _, command := range commands {
		if !strings.Contains(command, "go test") {
			t.Fatalf("a step that sets CARVE_SPEC_CORPUS runs %q, which does not run go test", command)
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
			// No filter: everything the package defines runs. This is the shape
			// both corpus steps have today.
			continue
		}

		// Go's -run matches the pattern against the test name unanchored, so
		// this mirrors it rather than requiring a full match.
		filter, err := regexp.Compile(pattern)
		if err != nil {
			t.Fatalf("a corpus step's -run pattern %q does not compile: %v", pattern, err)
		}
		var unreachable []string
		for _, name := range gated {
			if !filter.MatchString(name) {
				unreachable = append(unreachable, name)
			}
		}
		if len(unreachable) > 0 {
			t.Fatalf("a corpus step runs `go test -run %s`, which never executes %s. "+
				"These tests need CARVE_SPEC_CORPUS and only the corpus steps set it, so under this "+
				"filter there is no configuration in which they run (markup-carve/carve-go#29). Widen "+
				"the pattern or drop it.", pattern, strings.Join(unreachable, ", "))
		}
	}

	// The ablation. Without it the loop above passes identically whether the
	// pattern was applied or quietly ignored.
	if regexp.MustCompile("TestSpecCorpus").MatchString("TestEveryRecordedNodeTypeStillReachesTheTree") {
		t.Fatal("the filter check is not matching names")
	}
}
