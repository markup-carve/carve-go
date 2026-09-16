package carve

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// includeTree writes a small book layout and returns its root.
//
//	root/
//	  posts/main.crv      the document under test
//	  posts/child.crv     a sibling
//	  posts/a.crv b.crv   a two-file cycle
//	  posts/deep.crv      includes ../shared/glossary.crv
//	  shared/glossary.crv a sibling-directory target
//	outside.crv           written OUTSIDE the root
func includeTree(t *testing.T) (root string, outside string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "root")
	mkdir(t, filepath.Join(root, "posts"))
	mkdir(t, filepath.Join(root, "shared"))
	write(t, filepath.Join(root, "posts", "main.crv"), "Main.\n")
	write(t, filepath.Join(root, "posts", "child.crv"), "Child body.\n")
	write(t, filepath.Join(root, "posts", "deep.crv"), "Deep.\n\n{{ ../shared/glossary.crv }}\n")
	write(t, filepath.Join(root, "posts", "a.crv"), "Cycle A.\n\n{{ b.crv }}\n")
	write(t, filepath.Join(root, "posts", "b.crv"), "Cycle B.\n\n{{ a.crv }}\n")
	write(t, filepath.Join(root, "shared", "glossary.crv"), "Glossary body.\n")
	outside = filepath.Join(base, "outside.crv")
	write(t, outside, "SECRET\n")
	return root, outside
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func write(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func renderInclude(t *testing.T, root, doc, source string) IncludeResult {
	t.Helper()
	res, err := RenderWithIncludes(source, OutputHTML, Options{}, Include{Root: root, SourcePath: doc})
	if err != nil {
		t.Fatalf("RenderWithIncludes: %v", err)
	}
	return res
}

func rules(res IncludeResult) []string {
	out := make([]string, 0, len(res.Warnings))
	for _, w := range res.Warnings {
		out = append(out, w.Rule)
	}
	return out
}

// A sibling file is read, and its content replaces the directive.
func TestIncludeExpandsASibling(t *testing.T) {
	root, _ := includeTree(t)
	doc := filepath.Join(root, "posts", "main.crv")
	res := renderInclude(t, root, doc, "Before.\n\n{{ ./child.crv }}\n")

	if !strings.Contains(res.Output, "<p>Child body.</p>") {
		t.Fatalf("child was not expanded:\n%s", res.Output)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("want no warnings, got %v", res.Warnings)
	}
	want := []IncludeDependency{{Path: filepath.Join(root, "posts", "child.crv"), Resolved: true}}
	if !equalDeps(res.Dependencies, want) {
		t.Fatalf("dependencies = %v, want %v", res.Dependencies, want)
	}
}

// The in-memory source stands in for the file at SourcePath, and relative
// resolution still keys off that path's DIRECTORY rather than the root.
func TestIncludeServesTheInMemorySourceAtItsPath(t *testing.T) {
	root, _ := includeTree(t)
	doc := filepath.Join(root, "posts", "main.crv")
	res := renderInclude(t, root, doc, "Front matter was stripped.\n\n{{ ./child.crv }}\n")

	if !strings.Contains(res.Output, "Front matter was stripped.") {
		t.Fatalf("the caller's source did not reach the engine:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "<p>Main.</p>") {
		t.Fatalf("the engine read the file on disk instead of the caller's source:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "<p>Child body.</p>") {
		t.Fatalf("relative include did not resolve from the document's directory:\n%s", res.Output)
	}
}

// A nested include resolves relative to the file that CONTAINS it, not to the
// root and not to the document that pulled it in.
func TestIncludeResolvesANestedRelativePathAgainstItsOwnFile(t *testing.T) {
	root, _ := includeTree(t)
	doc := filepath.Join(root, "posts", "main.crv")
	res := renderInclude(t, root, doc, "Top.\n\n{{ ./deep.crv }}\n")

	if !strings.Contains(res.Output, "<p>Glossary body.</p>") {
		t.Fatalf("nested relative include did not resolve:\n%s\nwarnings %v", res.Output, res.Warnings)
	}
	want := []IncludeDependency{
		{Path: filepath.Join(root, "posts", "deep.crv"), Resolved: true},
		{Path: filepath.Join(root, "shared", "glossary.crv"), Resolved: true},
	}
	if !equalDeps(res.Dependencies, want) {
		t.Fatalf("dependencies = %v, want %v", res.Dependencies, want)
	}
}

// A sibling DIRECTORY inside the root is reachable through "..": containment is
// canonical, not a lexical ban on the component.
func TestIncludeReachesASiblingDirectoryInsideTheRoot(t *testing.T) {
	root, _ := includeTree(t)
	doc := filepath.Join(root, "posts", "main.crv")
	res := renderInclude(t, root, doc, "Top.\n\n{{ ../shared/glossary.crv }}\n")

	if !strings.Contains(res.Output, "<p>Glossary body.</p>") {
		t.Fatalf("sibling directory was refused:\n%s\nwarnings %v", res.Output, res.Warnings)
	}
}

// "../" past the root is refused, the directive stays literal, and the target's
// content does not appear.
func TestIncludeRefusesTraversalAboveTheRoot(t *testing.T) {
	root, _ := includeTree(t)
	doc := filepath.Join(root, "posts", "main.crv")
	res := renderInclude(t, root, doc, "Top.\n\n{{ ../../outside.crv }}\n")

	if strings.Contains(res.Output, "SECRET") {
		t.Fatalf("traversal escaped the root:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "{{ ../../outside.crv }}") {
		t.Fatalf("a refused directive must stay literal:\n%s", res.Output)
	}
	if got := rules(res); len(got) != 1 || got[0] != "include-unresolved" {
		t.Fatalf("rules = %v, want [include-unresolved]", got)
	}
}

// An absolute directive path is refused even when it names a file the mount can
// see, because the resolver does not allow absolute spellings.
func TestIncludeRefusesAnAbsolutePath(t *testing.T) {
	root, _ := includeTree(t)
	doc := filepath.Join(root, "posts", "main.crv")
	res := renderInclude(t, root, doc, "Top.\n\n{{ /carve-root/shared/glossary.crv }}\n")

	if strings.Contains(res.Output, "<p>Glossary body.</p>") {
		t.Fatalf("an absolute directive path was honored:\n%s", res.Output)
	}
	if got := rules(res); len(got) != 1 || got[0] != "include-unresolved" {
		t.Fatalf("rules = %v, want [include-unresolved]", got)
	}
}

// A symlink inside the root pointing outside it is refused. This is the case the
// mount alone does NOT cover: wazero follows such a link on a plain read, so the
// refusal has to come from the engine's canonical containment check.
//
// The two subtests are not equally strong, and the weaker one is kept for what
// it does pin. Widening the mount by one directory makes the RELATIVE link
// resolve and the subtest go red; the ABSOLUTE one stays green either way,
// because its link text is a host path and no host path is addressable in the
// guest namespace. It pins that the escape does not happen, not that carve-go
// would notice a wider mount.
func TestIncludeRefusesASymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	root, outside := includeTree(t)
	doc := filepath.Join(root, "posts", "main.crv")

	for _, tc := range []struct {
		name   string
		link   string
		target string
	}{
		{"absolute", "abs.crv", outside},
		{"relative", "rel.crv", "../../outside.crv"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			link := filepath.Join(root, "posts", tc.link)
			if err := os.Symlink(tc.target, link); err != nil {
				t.Skipf("symlink unsupported: %v", err)
			}
			t.Cleanup(func() { _ = os.Remove(link) })

			res := renderInclude(t, root, doc, "Top.\n\n{{ ./"+tc.link+" }}\n")
			if strings.Contains(res.Output, "SECRET") {
				t.Fatalf("a symlink escaped the root:\n%s", res.Output)
			}
			if got := rules(res); len(got) != 1 || got[0] != "include-unresolved" {
				t.Fatalf("rules = %v, want [include-unresolved]", got)
			}
		})
	}
}

// A cycle is broken, reported against the file the second reference was written
// in, and leaves that directive literal.
func TestIncludeBreaksACycle(t *testing.T) {
	root, _ := includeTree(t)
	doc := filepath.Join(root, "posts", "main.crv")
	res := renderInclude(t, root, doc, "Top.\n\n{{ ./a.crv }}\n")

	if got := rules(res); len(got) != 1 || got[0] != "include-cycle" {
		t.Fatalf("rules = %v, want [include-cycle]", got)
	}
	if want := filepath.Join(root, "posts", "b.crv"); res.Warnings[0].File != want {
		t.Fatalf("warning file = %q, want %q", res.Warnings[0].File, want)
	}
	if !strings.Contains(res.Output, "<p>Cycle A.</p>") || !strings.Contains(res.Output, "<p>Cycle B.</p>") {
		t.Fatalf("the two files before the cycle should still be expanded:\n%s", res.Output)
	}
	if strings.Count(res.Output, "Cycle A.") != 1 {
		t.Fatalf("the cycle re-entered:\n%s", res.Output)
	}
}

// A missing target is reported, stays literal, and is still listed as a
// dependency so a host learns when it starts existing.
func TestIncludeReportsAMissingTarget(t *testing.T) {
	root, _ := includeTree(t)
	doc := filepath.Join(root, "posts", "main.crv")
	res := renderInclude(t, root, doc, "Top.\n\n{{ ./nope.crv }}\n")

	if got := rules(res); len(got) != 1 || got[0] != "include-unresolved" {
		t.Fatalf("rules = %v, want [include-unresolved]", got)
	}
	if !strings.Contains(res.Output, "{{ ./nope.crv }}") {
		t.Fatalf("a missing target must stay literal:\n%s", res.Output)
	}
	want := []IncludeDependency{{Path: "./nope.crv"}}
	if !equalDeps(res.Dependencies, want) {
		t.Fatalf("dependencies = %v, want %v", res.Dependencies, want)
	}
}

// Past the engine's 100-warning cap, one warning per rule survives and the rest
// are counted rather than dropped silently.
func TestIncludeReportsSuppressedWarnings(t *testing.T) {
	root, _ := includeTree(t)
	doc := filepath.Join(root, "posts", "main.crv")

	var b strings.Builder
	b.WriteString("Top.\n")
	for i := 0; i < 150; i++ {
		fmt.Fprintf(&b, "\n{{ ./nope-%d.crv }}\n", i)
	}
	res := renderInclude(t, root, doc, b.String())

	if res.SuppressedWarnings != 50 {
		t.Fatalf("SuppressedWarnings = %d, want 50", res.SuppressedWarnings)
	}
	if len(res.Warnings) != 100 {
		t.Fatalf("len(Warnings) = %d, want the engine's cap of 100", len(res.Warnings))
	}
	// Refused dependencies are read off the warnings, so they are capped with
	// them. Pinned rather than glossed: a caller reading Dependencies past the
	// cap is holding a sample, and SuppressedWarnings is how it knows.
	if len(res.Dependencies) != 100 {
		t.Fatalf("len(Dependencies) = %d, want the 100 the retained warnings name", len(res.Dependencies))
	}
}

// No host path reaches a warning message (spec I7). The root's own directory
// name is unguessable here, so its presence would mean the resolver's error text
// leaked through.
func TestIncludeWarningsCarryNoHostPath(t *testing.T) {
	root, _ := includeTree(t)
	doc := filepath.Join(root, "posts", "main.crv")
	res := renderInclude(t, root, doc, "Top.\n\n{{ ../../outside.crv }}\n")

	for _, w := range res.Warnings {
		if strings.Contains(w.Message, root) || strings.Contains(w.Message, guestRoot) {
			t.Fatalf("warning message leaks a path: %q", w.Message)
		}
	}
}

// Without an Include the directive is left alone, which is what the conformance
// corpus pins for every engine.
func TestDirectivesStayLiteralWithoutAnInclude(t *testing.T) {
	html, err := ToHTML("Top.\n\n{{ ./child.crv }}\n")
	if err != nil {
		t.Fatalf("ToHTML: %v", err)
	}
	if !strings.Contains(html, "{{ ./child.crv }}") {
		t.Fatalf("a directive must stay literal with no resolver:\n%s", html)
	}
}

// A relative root is refused rather than absolutized: resolving it would make
// containment depend on the process working directory.
func TestIncludeRefusesARelativeRoot(t *testing.T) {
	_, err := RenderWithIncludes("Top.\n", OutputHTML, Options{},
		Include{Root: "content", SourcePath: "/abs/content/a.crv"})
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("error = %v, want one naming the absolute requirement", err)
	}
}

func TestIncludeRefusesASourcePathOutsideTheRoot(t *testing.T) {
	root, outside := includeTree(t)
	_, err := RenderWithIncludes("Top.\n", OutputHTML, Options{},
		Include{Root: root, SourcePath: outside})
	if err == nil || !strings.Contains(err.Error(), "not inside") {
		t.Fatalf("error = %v, want one naming the containment failure", err)
	}
}

func TestIncludeRefusesAnEmptyRootOrSourcePath(t *testing.T) {
	if _, err := RenderWithIncludes("x", OutputHTML, Options{}, Include{SourcePath: "/a/b.crv"}); err == nil {
		t.Fatal("an empty Root must be refused")
	}
	if _, err := RenderWithIncludes("x", OutputHTML, Options{}, Include{Root: "/a"}); err == nil {
		t.Fatal("an empty SourcePath must be refused")
	}
}

// Includes are not an HTML-only pass: the engine expands for every target but
// canonical Carve, which spec I15 excludes so the formatter round-trips source.
func TestIncludeExpandsForMarkdownAndNotForCarve(t *testing.T) {
	root, _ := includeTree(t)
	doc := filepath.Join(root, "posts", "main.crv")

	md, err := RenderWithIncludes("Top.\n\n{{ ./child.crv }}\n", OutputMarkdown, Options{},
		Include{Root: root, SourcePath: doc})
	if err != nil {
		t.Fatalf("markdown: %v", err)
	}
	if !strings.Contains(md.Output, "Child body.") {
		t.Fatalf("markdown did not expand:\n%s", md.Output)
	}

	crv, err := RenderWithIncludes("Top.\n\n{{ ./child.crv }}\n", OutputCarve, Options{},
		Include{Root: root, SourcePath: doc})
	if err != nil {
		t.Fatalf("carve: %v", err)
	}
	if !strings.Contains(crv.Output, "{{ ./child.crv }}") {
		t.Fatalf("canonical Carve must leave the directive alone:\n%s", crv.Output)
	}
}

func equalDeps(got, want []IncludeDependency) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
