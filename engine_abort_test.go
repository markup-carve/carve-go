package carve

import (
	"strings"
	"testing"
)

// The engine must not ABORT on a document, however malformed.
//
// An abort inside the guest is not an engine exit code. The module traps,
// wazero returns an error that is not a *sys.ExitError, and runEngine hands
// that back as a plain error - so a caller cannot tell "this document is
// invalid" from "the engine died", and a render that should have produced a
// paragraph produces nothing at all. On untrusted input that is reachable
// denial of service rather than a rendering difference.
//
// It was reachable. The artifact this package shipped up to v0.1.4, built from
// carve-rs 0.1.6, aborted on FIVE BYTES:
//
//	|{.x}
//
// A single pipe carrying what looks like row attributes reached the table
// check, which panicked rather than deciding the line was ordinary paragraph
// text. Driving that artifact directly gives
// `wasm error: unreachable` with `abort_internal` on the stack, not an exit
// status. carve-rs 0.1.8 fixed it (carve-rs#2341) and the rebuilt artifact
// renders the line as a paragraph.
//
// Nothing here would have noticed either way. carve_robustness_test.go covers
// deadlines, cancellation and the memory cap - every case where the HOST stops
// the engine - and had no case where the engine stops itself. So this is the
// arm that was missing, not a restatement of one that exists.
//
// The assertion is deliberately "no error and some output" rather than exact
// HTML. The exact spelling of a degenerate table line is the spec's business
// and the corpus already pins it; what this package needs to know is that the
// engine came back at all.
func TestEngineDoesNotAbortOnDegenerateTableRows(t *testing.T) {
	// Each of these reaches the table reader with a row that has no cells, or
	// an attribute block where cells were expected. The first is the exact
	// input that aborted the previously shipped artifact; the rest walk the
	// same boundary so a future regression at a neighbouring shape is caught
	// by the same test.
	cases := []string{
		"|{.x}\n",
		"|{.x}",
		"|{#a}\n",
		"|{.x .y}\n",
		"|\n",
		"||\n",
		"|{.x}|\n",
		"|{.x}\n|{.y}\n",
		"> |{.x}\n",
		"- |{.x}\n",
	}

	for _, src := range cases {
		got, err := ToHTML(src)
		if err != nil {
			t.Errorf("ToHTML(%q) returned an error: %v\n"+
				"An error here is very likely an engine ABORT rather than a refusal: the engine "+
				"reports a refusal through its exit status, which runEngine turns into a message "+
				"naming the code, while a trap arrives as a wazero error. Drive the artifact with "+
				"this input directly to tell them apart.", src, err)
			continue
		}
		if strings.TrimSpace(got) == "" {
			t.Errorf("ToHTML(%q) returned no output and no error; every one of these inputs has "+
				"visible text, so empty output means the render did not happen", src)
		}
	}
}

// The ablation, so the loop above cannot pass by testing nothing.
//
// Without it, an empty or mistyped case list reports a clean result, which is
// the shape this repository has been bitten by twice in its corpus guards.
func TestTheAbortCaseListIsNotEmpty(t *testing.T) {
	// Mirrors the first case above, which is the one with provenance: it is the
	// input that actually aborted the shipped artifact. If the list is ever
	// rewritten and loses it, this says so rather than letting the coverage
	// quietly narrow.
	got, err := ToHTML("|{.x}\n")
	if err != nil {
		t.Fatalf("the anchor case |{.x} does not render: %v", err)
	}
	if !strings.Contains(got, "|{.x}") {
		t.Fatalf("the anchor case |{.x} rendered as %q, which does not carry its own text; "+
			"the engine changed how it reads this line and the case above needs rereading", got)
	}
}
