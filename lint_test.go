package carve

import (
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
		if findings[i] != want[i] {
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
	if len(got) != 1 || got[0] != want {
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
