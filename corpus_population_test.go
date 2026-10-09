package carve

import (
	"os"
	"os/exec"
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
// generated from the `::: compare` blocks in resources/examples/{core,
// extensions,edge-cases}.md (see tests/corpus/README.md and
// scripts/generate-corpus.mjs in the spec repository); the generator refuses to
// write a corpus where the two disagree. Both live in the same spec checkout CI
// already clones, one directory away from CARVE_SPEC_CORPUS - the same route
// corpus_ast_test.go already uses to read resources/ast-schema.json.
//
// THE PAGES MOVED, AND THIS HELPER DID NOT. They lived under docs/examples/
// until markup-carve/carve#1194 made them generated sources and filed them
// beside the other generator inputs in resources/. Because the path is read at
// run time and the miss is fatal, the corpus job stopped comparing anything at
// all rather than comparing against a stale count - the right failure, but it
// masked every real divergence behind it until the path moved too. The route
// corpus_ast_test.go takes to resources/ast-schema.json is now the same route
// this helper takes, which is one fewer place for the two to disagree.
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
// Count Carve and HTML fences independently of generated files. Each block
// must contain equal, nonzero counts; literal fenced content is ignored.
func declaredCorpusSize(t *testing.T, corpusDir string) int {
	t.Helper()
	examplesDir := filepath.Join(corpusDir, "..", "..", "resources", "examples")
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
		marker, fence := "", ""
		carveCount, htmlCount := 0, 0
		for _, line := range strings.Split(string(blob), "\n") {
			if fence != "" {
				if strings.HasPrefix(line, fence) && strings.TrimSpace(line[len(fence):]) == "" {
					fence = ""
				}
				continue
			}
			ticks := len(line) - len(strings.TrimLeft(line, "`"))
			if ticks >= 3 {
				fence = line[:ticks]
				if marker != "" {
					switch strings.TrimSpace(line[ticks:]) {
					case "carve":
						carveCount++
					case "html":
						htmlCount++
					}
				}
				continue
			}
			trimmed := strings.TrimSpace(line)
			if marker != "" {
				if trimmed == marker {
					if carveCount == 0 || carveCount != htmlCount {
						t.Fatalf("unpaired or empty compare block in %s: carve=%d html=%d", path, carveCount, htmlCount)
					}
					declared += carveCount
					marker = ""
				}
				continue
			}
			if compareOpenLine.MatchString(trimmed) {
				marker = compareMarkerRun.FindString(trimmed)
				carveCount, htmlCount = 0, 0
			}
		}
		if marker != "" || fence != "" {
			t.Fatalf("unclosed compare block or fence in %s", path)
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
			"resources/examples/{core,extensions,edge-cases}.md yields one corpus pair per carve fence, so a "+
			"difference "+
			"means the corpus at %s is not the one those pages describe - a truncated or stale "+
			"checkout, a wrong CARVE_SPEC_CORPUS, or a corpus that needs regenerating "+
			"(npm run corpus:build in the spec repository). It does not mean this run was clean.",
			what, got, declared, corpusDir)
	}
}

func TestDeclaredCorpusCountsPairsAndIgnoresFencedMarkup(t *testing.T) {
	root := t.TempDir()
	examples := filepath.Join(root, "resources", "examples")
	if err := os.MkdirAll(examples, 0755); err != nil {
		t.Fatal(err)
	}
	source := "````text\n::: compare\n```carve\nfake\n```\n```html\nfake\n```\n:::\n````\n::: compare no-render\n````carve\n::: compare\n```html\nliteral\n```\n:::\n````\n```html\n<p>first</p>\n```\n```carve\nsecond\n```\n```html\n<p>second</p>\n```\n:::\n"
	for i, page := range specExamplePages {
		content := ""
		if i == 0 {
			content = source
		}
		if err := os.WriteFile(filepath.Join(examples, page), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	corpus := filepath.Join(root, "tests", "corpus")
	if got := declaredCorpusSize(t, corpus); got != 2 {
		t.Fatalf("got %d pairs, want 2", got)
	}
	requireWholeCorpus(t, corpus, 2, "complete")
}

// A block holding SEVERAL pairs is the case the old block-counting loop could
// not see, and the fixture above cannot distinguish: it reports 2 under either
// counter. Three pairs in one block separate them.
func TestDeclaredCorpusCountsEveryPairInOneBlock(t *testing.T) {
	root := t.TempDir()
	examples := filepath.Join(root, "resources", "examples")
	if err := os.MkdirAll(examples, 0755); err != nil {
		t.Fatal(err)
	}
	source := strings.Join([]string{
		"::: compare",
		"```carve", "one", "```", "```html", "<p>one</p>", "```",
		"````carve", "```carve", "nested, not a pair", "```", "````", "```html", "<pre>two</pre>", "```",
		"```carve", "three", "```", "```html", "<p>three</p>", "```",
		":::",
		"```carve", "outside any block", "```",
		"",
	}, "\n")
	for i, page := range specExamplePages {
		content := ""
		if i == 0 {
			content = source
		}
		if err := os.WriteFile(filepath.Join(examples, page), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	corpus := filepath.Join(root, "tests", "corpus")
	if got := declaredCorpusSize(t, corpus); got != 3 {
		t.Fatalf("got %d pairs, want 3", got)
	}
}

func TestCorpusPopulationRefusals(t *testing.T) {
	if mode := os.Getenv("CARVE_POPULATION_TEST_MODE"); mode != "" {
		root := t.TempDir()
		examples := filepath.Join(root, "resources", "examples")
		if err := os.MkdirAll(examples, 0755); err != nil {
			t.Fatal(err)
		}
		source := "::: compare\n```carve\nx\n```\n```html\nx\n```\n:::\n"
		switch mode {
		case "unpaired":
			source = "::: compare\n```carve\nx\n```\n:::\n"
		case "empty":
			source = "::: compare\n:::\n"
		case "unclosed":
			source = "::: compare\n"
		}
		for i, page := range specExamplePages {
			content := ""
			if i == 0 {
				content = source
			}
			if err := os.WriteFile(filepath.Join(examples, page), []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
		}
		corpus := filepath.Join(root, "tests", "corpus")
		if mode == "truncated" {
			requireWholeCorpus(t, corpus, 0, "truncated")
		} else {
			declaredCorpusSize(t, corpus)
		}
		return
	}
	for _, test := range []struct{ mode, message string }{
		{"truncated", "spec's example pages declare 1"},
		{"unpaired", "unpaired or empty compare block"},
		{"empty", "unpaired or empty compare block"},
		{"unclosed", "unclosed compare block or fence"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestCorpusPopulationRefusals$")
			cmd.Env = append(os.Environ(), "CARVE_POPULATION_TEST_MODE="+test.mode)
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), test.message) {
				t.Fatalf("expected %s refusal, got err=%v output=%s", test.mode, err, output)
			}
		})
	}
}
