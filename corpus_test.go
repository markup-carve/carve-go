package carve

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The mandatory spec corpus, run through the EMBEDDED engine.
//
// Every implementation is held to byte-identical HTML for these inputs, and
// this package ships a prebuilt wasm rather than compiling the engine, so the
// artifact can silently fall behind the spec. It did: before the rebuild in
// this commit's PR, the committed wasm still stopped a link destination at the
// first ")" and did not know the inline-literal "!" prefix. Nothing in CI
// noticed, because the rest of the suite asserts hand-written expectations.
//
// The corpus path comes from CARVE_SPEC_CORPUS. When unset the test skips, so
// `go test ./...` works in a plain checkout without the spec. CI always sets
// it, and the guards below turn "the corpus wasn't really there" into a
// failure rather than a pass.
func TestSpecCorpus(t *testing.T) {
	dir := os.Getenv("CARVE_SPEC_CORPUS")
	if dir == "" {
		t.Skip("CARVE_SPEC_CORPUS not set; see .github/workflows/ci.yml for the corpus job")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("CARVE_SPEC_CORPUS=%s is not readable: %v", dir, err)
	}

	var mismatches []string
	total := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".crv") {
			continue
		}
		base := strings.TrimSuffix(name, ".crv")
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want, err := os.ReadFile(filepath.Join(dir, base+".html"))
		if err != nil {
			// A .crv with no .html pair is not an input to this comparison.
			continue
		}
		total++

		got, err := ToHTML(string(src))
		if err != nil {
			mismatches = append(mismatches, base+": render error: "+err.Error())
			continue
		}
		if strings.TrimRight(got, "\n") != strings.TrimRight(string(want), "\n") {
			mismatches = append(mismatches, base)
		}
	}

	// Without this, an empty or wrong directory would report zero mismatches
	// and pass -- the exact shape of check that let the stale artifact through.
	// It used to be `total < 400` against a corpus of 830, which passed over a
	// corpus missing half its documents; see corpus_population_test.go for what
	// replaced it and why the reference is the spec's example pages rather than
	// a number recorded here.
	requireWholeCorpus(t, dir, total, "corpus pairs compared")

	sort.Strings(mismatches)
	if len(mismatches) > 0 {
		for i, m := range mismatches {
			if i >= 20 {
				t.Errorf("... and %d more", len(mismatches)-20)
				break
			}
			t.Errorf("corpus mismatch: %s", m)
		}
		t.Fatalf("%d of %d corpus cases diverge from the spec; the embedded wasm probably needs a rebuild (./build-wasm.sh)", len(mismatches), total)
	}
	t.Logf("%d corpus cases byte-identical", total)
}
