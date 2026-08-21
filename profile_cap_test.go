package carve

import (
	"strconv"
	"strings"
	"testing"
)

// A profile's max_length is a REFUSAL, not an empty render.
//
// The embedded engine used to answer an over-cap document with empty stdout and
// exit status 0, so this package handed a caller `("", nil)`: a refusal that
// reports success, which every automated consumer reads as "the document
// legitimately rendered to nothing". markup-carve/carve-rs#1194 made it a
// non-zero exit with the violation on stderr, and this file pins that through
// the embedded artifact (markup-carve/carve-go#56).
//
// EVERY assertion here is on the ERROR. An assertion that the render is empty
// is worthless for this bug, because empty output is the bug's own symptom -
// it stays green on both sides of the fix. The at-cap cases exist for the
// opposite reason: without them, an engine that had simply stopped rendering
// anything under a profile would satisfy the over-cap rows.
//
// The two numbers are spelled out rather than read from the engine, so the test
// states the limit it is about. They are `Profile::COMMENT_MAX_LENGTH` and
// `Profile::MINIMAL_MAX_LENGTH` in carve-rs `src/profile.rs`; the engine is
// what enforces them, and markup-carve/hugo-carve#21 is what happens when a
// caller has to restate them to defend itself.
const (
	commentMaxLength = 100_000
	minimalMaxLength = 10_000
)

var profileCaps = []struct {
	profile string
	max     int
}{
	{"comment", commentMaxLength},
	{"minimal", minimalMaxLength},
}

// TestProfileCap_OverCapIsAnError is the regression this file exists for.
func TestProfileCap_OverCapIsAnError(t *testing.T) {
	for _, c := range profileCaps {
		t.Run(c.profile, func(t *testing.T) {
			src := strings.Repeat("x", c.max+1)

			out, err := ToHTMLOptions(src, Options{Profile: c.profile})
			if err == nil {
				t.Fatalf("%s: a %d-byte document is over the %d-byte cap and returned no error (%d bytes of output); "+
					"a refusal reported as success is indistinguishable from an empty render",
					c.profile, len(src), c.max, len(out))
			}
			// The engine's own words reach the caller, so nothing downstream
			// has to guess WHY the document was refused.
			if !strings.Contains(err.Error(), "max_length_exceeded") {
				t.Fatalf("%s: the error does not name the machine reason: %v", c.profile, err)
			}
			if !strings.Contains(err.Error(), strconv.Itoa(c.max)) {
				t.Fatalf("%s: the error does not name the limit it enforced: %v", c.profile, err)
			}
		})
	}
}

// TestProfileCap_AtCapStillRenders is what makes the test above able to fail.
// An engine that refused everything under a profile - or one that had stopped
// rendering at all - would satisfy every assertion up there.
func TestProfileCap_AtCapStillRenders(t *testing.T) {
	for _, c := range profileCaps {
		t.Run(c.profile, func(t *testing.T) {
			src := strings.Repeat("x", c.max)

			out, err := ToHTMLOptions(src, Options{Profile: c.profile})
			if err != nil {
				t.Fatalf("%s: a document of exactly %d bytes is AT the cap, not over it: %v", c.profile, c.max, err)
			}
			if !strings.Contains(out, src) {
				t.Fatalf("%s: an at-cap document rendered %d bytes, which do not contain the input", c.profile, len(out))
			}
		})
	}
}

// TestProfileCap_RefusesOnEveryRenderTarget widens the pin to the formats
// markup-carve/carve-go#54 added. The engine's own regression test walks the
// same list, and `--carve` is on it only because markup-carve/carve-rs#1198
// put the profile on that path too: `to_carve` took no options, so an over-cap
// document used to be re-serialized in full at exit 0 there even after the
// other five targets started refusing.
func TestProfileCap_RefusesOnEveryRenderTarget(t *testing.T) {
	formats := []OutputFormat{OutputHTML, OutputMarkdown, OutputPlainText, OutputANSI, OutputCarve}
	src := strings.Repeat("x", minimalMaxLength+1)

	for _, f := range formats {
		t.Run(f.flag(), func(t *testing.T) {
			out, err := Render(src, f, Options{Profile: "minimal"})
			if err == nil {
				t.Fatalf("%s: an over-cap document returned no error (%d bytes of output)", f.flag(), len(out))
			}
			if !strings.Contains(err.Error(), "max_length_exceeded") {
				t.Fatalf("%s: the error does not name the machine reason: %v", f.flag(), err)
			}
		})
	}
}
