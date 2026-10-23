package carve

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The engine has had `carve lint` since before the previously pinned revision.
// Nothing in this package reached it, so every lint rule the embedded artifact
// carries was unobservable from Go (#80).
func TestLintReportsFindingsWithPositionAndRule(t *testing.T) {
	findings, err := Lint("# T\n\nSee [a](#no) and [b](#nope).\n\nAnd [c][undef].\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []LintFinding{
		{Path: "<stdin>", Line: 3, Column: 5, Rule: "broken-fragment-link",
			Message: `Link to "#no" matches no id in this document, so the link goes nowhere.`},
		{Path: "<stdin>", Line: 3, Column: 18, Rule: "broken-fragment-link",
			Message: `Link to "#nope" matches no id in this document, so the link goes nowhere.`},
		{Path: "<stdin>", Line: 5, Column: 5, Rule: "unresolved-reference-link",
			Message: "Reference link has no matching definition or heading."},
	}
	if len(findings) != len(want) {
		t.Fatalf("got %d findings, want %d: %#v", len(findings), len(want), findings)
	}
	for i := range want {
		if !reflect.DeepEqual(findings[i], want[i]) {
			t.Errorf("finding %d:\n got %#v\nwant %#v", i, findings[i], want[i])
		}
	}
}

// Findings are the answer to the question, not a failure to ask it: the engine
// signals them with exit 1, which ReadStamp already treats as an answer rather
// than an error. A clean document returns an empty, non-nil slice so a caller
// can range over it without a nil check.
func TestLintSeparatesFindingsFromFailure(t *testing.T) {
	clean, err := Lint("# Title\n\nBody.\n")
	if err != nil {
		t.Fatalf("a clean document must not be an error: %v", err)
	}
	if clean == nil {
		t.Error("a clean document must return an empty slice, not nil")
	}
	if len(clean) != 0 {
		t.Errorf("a clean document must report nothing, got %#v", clean)
	}

	if empty, err := Lint(""); err != nil || len(empty) != 0 {
		t.Errorf("an empty document: %#v, %v", empty, err)
	}

	// Exit 2 is the engine failing to run the pass, which IS an error. The
	// accepted extension keys are not duplicated here, so the message is the
	// engine's own.
	_, err = LintWithOptions("x\n", LintOptions{Extensions: []string{"nope"}})
	if err == nil || !strings.Contains(err.Error(), "unknown extension: nope") {
		t.Errorf("an unaccepted extension key must surface the engine's error, got %v", err)
	}
}

// --extension citations is the one key the lint pass accepts, and the bundle
// changes which rules run. Pinned because a caller selecting extensions gets a
// different finding set, which is surprising enough to hold to the measurement.
func TestLintExtensionSelectionReachesTheEngine(t *testing.T) {
	const src = "# T\n\nSee [a](#no) and [b](#nope).\n\nAnd [c][undef].\n"

	cited, err := LintWithOptions(src, LintOptions{Extensions: []string{"citations"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cited) != 3 {
		t.Errorf("citations alone must not change the core findings, got %#v", cited)
	}

	bundled, err := LintWithOptions(src, LintOptions{Bundle: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundled) != 1 || bundled[0].Rule != "unresolved-reference-link" {
		t.Errorf("the bundle resolves the fragment links; got %#v", bundled)
	}
}

// A findings line this package cannot read is an error rather than a silently
// dropped finding, so an engine that changes the line format says so.
func TestLintRefusesAnUnreadableFindingsLine(t *testing.T) {
	if _, err := parseLintFindings("not a findings line\n"); err == nil {
		t.Error("an unparsable line must be an error")
	}
	// The engine separates rule from message with an em dash, so a reader
	// splitting on " - " would see the whole tail as the rule name.
	got, err := parseLintFindings("a/b.crv:12:3 some-rule \u2014 Message with - a hyphen.\n")
	if err != nil {
		t.Fatal(err)
	}
	want := LintFinding{Path: "a/b.crv", Line: 12, Column: 3, Rule: "some-rule",
		Message: "Message with - a hyphen."}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

// A lint finding's column counts codepoints, like every other engine position.
func TestLintColumnsCountCodepointsNotBytes(t *testing.T) {
	for _, lead := range []string{"é", "\U0001F600"} {
		findings, err := Lint("# T\n\n" + lead + " [a](#no).\n")
		if err != nil || len(findings) != 1 {
			t.Fatalf("%q: %#v, %v", lead, findings, err)
		}
		if findings[0].Line != 3 || findings[0].Column != 3 {
			t.Errorf("%q: the column must not move with the byte length: %#v", lead, findings[0])
		}
	}
}

// With an Include the pass follows directives, as `carve lint --include-root`
// does: a child's finding names its file and the chain that reached it, and a
// directive that stays literal is a finding of its own.
func TestLintFollowsIncludesAndReportsTheChain(t *testing.T) {
	root := t.TempDir()
	mkdir(t, filepath.Join(root, "parts"))
	write(t, filepath.Join(root, "parts", "child.crv"), "See [x](#nope).\n")
	write(t, filepath.Join(root, "parts", "mid.crv"), "Mid.\n\n{{ child.crv }}\n")
	book := filepath.Join(root, "book.crv")
	source := "Own [y](#gone).\n\n{{ parts/mid.crv }}\n\n{{ missing.crv }}\n"

	got, err := LintWithOptions(source, LintOptions{Include: &Include{Root: root, SourcePath: book}})
	if err != nil {
		t.Fatal(err)
	}
	want := []LintFinding{
		{Path: book, Line: 1, Column: 5, Rule: "broken-fragment-link",
			Message:    `Link to "#gone" matches no id in this document, so the link goes nowhere.`,
			IncludedBy: []LintSite{}},
		{Path: filepath.Join(root, "parts", "child.crv"), Line: 1, Column: 5, Rule: "broken-fragment-link",
			Message: `Link to "#nope" matches no id in this document, so the link goes nowhere.`,
			File:    "parts/child.crv",
			IncludedBy: []LintSite{
				{File: "book.crv", Line: 3, Column: 1},
				{File: "parts/mid.crv", Line: 3, Column: 1},
			}},
	}
	if len(got) != 3 {
		t.Fatalf("want the own finding, the child's and the unresolved directive, got %#v", got)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("finding %d:\n got %#v\nwant %#v", i, got[i], want[i])
		}
	}
	if got[2].Rule != "include-unresolved" || got[2].Line != 5 {
		t.Errorf("the literal directive must be reported at its line, got %#v", got[2])
	}

	// Without an Include nothing is read: the directives are inert text.
	plain, err := Lint(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != 1 || plain[0].File != "" || plain[0].IncludedBy != nil {
		t.Errorf("without an Include only the own finding is reported, got %#v", plain)
	}
}
