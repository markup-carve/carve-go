package carve

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ONE spelling of "a corpus runner must not report success over an empty or
// short population" (the variant-2 defect catalogued in markup-carve/carve#755,
// with the shared helper and the contributor convention added by
// markup-carve/carve#955).
//
// This package had two of them, each with its own literal and its own wrong
// number: corpus_test.go said "the corpus has ~500" and corpus_ast_test.go said
// "~650", and both let a run through at 400 documents. The corpus has 830. That
// is not a rounding error, it is a guard that accepts a corpus missing half its
// documents: measured before this change, a corpus with every fourth document
// deleted (623 of 830) passed the whole package.
//
// THE COMPARISON HAS TO BE AGAINST SOMETHING THE RUNNER DOES NOT ITSELF WRITE.
// Deriving "how many documents should there be" from the directory being
// checked would be variant 1 (a check that reads its own frozen input) hiding
// inside a variant 2 fix - emptying the directory would move both sides and the
// guard would still pass. It was made and caught that way in
// markup-carve/pandoc-carve.
//
// So the reference is the corpus's SOURCE, not the corpus. tests/corpus is
// generated from the `::: compare` blocks in docs/examples/{core,extensions,
// edge-cases}.md (see tests/corpus/README.md and scripts/generate-corpus.mjs in
// the spec repository); the generator refuses to write a corpus where the two
// disagree. Both live in the same spec checkout CI already clones, one
// directory away from CARVE_SPEC_CORPUS - the same route corpus_ast_test.go
// already uses to read resources/ast-schema.json.
//
// Counting the source rather than recording a number also means there is no
// literal left to go stale: adding an example moves the expectation on the next
// corpus rebuild, without anyone editing this file.

// The pages the corpus is generated from, in the order the generator reads
// them. Order is irrelevant to a count; the list is the generator's.
var specExamplePages = []string{"core.md", "extensions.md", "edge-cases.md"}

// Mirrors generate-corpus.mjs: `::: compare`, or a longer colon run, with
// optional modifiers such as `::: compare no-render`.
var compareOpenLine = regexp.MustCompile(`^:{3,}\s+compare(\s+\S.*)?$`)

var compareMarkerRun = regexp.MustCompile(`^:{3,}`)

// declaredCorpusSize counts the example pairs the spec DECLARES, by reading the
// pages tests/corpus is generated from. corpusDir is CARVE_SPEC_CORPUS, i.e.
// <spec>/tests/corpus.
//
// The scan mirrors the generator's state machine rather than grepping: a
// `::: compare` line inside an already-open compare block is content, not a
// second pair, and the generator closes a block on a bare marker line. Mirroring
// keeps the two counts equal by construction instead of by luck.
func declaredCorpusSize(t *testing.T, corpusDir string) int {
	t.Helper()
	examplesDir := filepath.Join(corpusDir, "..", "..", "docs", "examples")
	declared := 0
	for _, page := range specExamplePages {
		path := filepath.Join(examplesDir, page)
		blob, err := os.ReadFile(path)
		if err != nil {
			// Not a soft skip. Without this file there is no independent
			// statement of how big the corpus should be, and a corpus check
			// with nothing to compare against is the failure shape this helper
			// exists to remove.
			t.Fatalf("no corpus source page at %s: %v. tests/corpus is generated from these pages; "+
				"if the spec moved them, this helper has to move with them", path, err)
		}
		inCompare := false
		marker := ""
		for _, line := range strings.Split(string(blob), "\n") {
			trimmed := strings.TrimSpace(line)
			if inCompare {
				if trimmed == marker {
					inCompare = false
					marker = ""
				}
				continue
			}
			if compareOpenLine.MatchString(trimmed) {
				declared++
				inCompare = true
				marker = compareMarkerRun.FindString(trimmed)
			}
		}
	}
	if declared == 0 {
		t.Fatalf("the corpus source pages under %s declare no ::: compare blocks at all; "+
			"this is a wiring problem, not a corpus of size zero", examplesDir)
	}
	return declared
}

// requireWholeCorpus is the only place this package decides whether a corpus
// population is big enough to draw a conclusion from. got is what the caller
// actually processed; what names it for the failure message.
//
// Equality rather than a floor, deliberately. A floor is what went stale twice
// here, and it answers the wrong question: "at least 400" cannot tell a whole
// corpus from a truncated checkout, and truncation is the failure being
// guarded against.
func requireWholeCorpus(t *testing.T, corpusDir string, got int, what string) {
	t.Helper()
	declared := declaredCorpusSize(t, corpusDir)
	if got != declared {
		t.Fatalf("%s: %d, but the spec's example pages declare %d. Every ::: compare block in "+
			"docs/examples/{core,extensions,edge-cases}.md becomes one corpus pair, so a difference "+
			"means the corpus at %s is not the one those pages describe - a truncated or stale "+
			"checkout, a wrong CARVE_SPEC_CORPUS, or a corpus that needs regenerating "+
			"(npm run corpus:build in the spec repository). It does not mean this run was clean.",
			what, got, declared, corpusDir)
	}
}
