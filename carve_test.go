package carve

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

func TestToHTML_Heading(t *testing.T) {
	out, err := ToHTML("# Hi")
	if err != nil {
		t.Fatalf("ToHTML error: %v", err)
	}
	if !strings.Contains(out, "<h1") {
		t.Fatalf("expected <h1 in output, got %q", out)
	}
}

func TestToHTML_Bold(t *testing.T) {
	// In Carve, *x* is strong (bold).
	out, err := ToHTML("*x*")
	if err != nil {
		t.Fatalf("ToHTML error: %v", err)
	}
	if !strings.Contains(out, "<strong>x</strong>") {
		t.Fatalf("expected <strong>x</strong>, got %q", out)
	}
}

func TestToHTML_Emphasis(t *testing.T) {
	// In Carve, /x/ is emphasis (italic).
	out, err := ToHTML("/x/")
	if err != nil {
		t.Fatalf("ToHTML error: %v", err)
	}
	if !strings.Contains(out, "<em>x</em>") {
		t.Fatalf("expected <em>x</em>, got %q", out)
	}
}

func TestToHTML_List(t *testing.T) {
	out, err := ToHTML("- a\n- b")
	if err != nil {
		t.Fatalf("ToHTML error: %v", err)
	}
	if !strings.Contains(out, "<ul>") || !strings.Contains(out, "<li>a</li>") {
		t.Fatalf("expected unordered list with items, got %q", out)
	}
}

func TestToHTML_Link(t *testing.T) {
	out, err := ToHTML("[link](https://example.com)")
	if err != nil {
		t.Fatalf("ToHTML error: %v", err)
	}
	if !strings.Contains(out, `<a href="https://example.com">link</a>`) {
		t.Fatalf("expected anchor, got %q", out)
	}
}

func TestToHTML_Table(t *testing.T) {
	out, err := ToHTML("| A | B |\n|---|---|\n| 1 | 2 |")
	if err != nil {
		t.Fatalf("ToHTML error: %v", err)
	}
	if !strings.Contains(out, "<table>") || !strings.Contains(out, "<th>A</th>") || !strings.Contains(out, "<td>1</td>") {
		t.Fatalf("expected table markup, got %q", out)
	}
}

func TestToHTML_EmptyNoPanic(t *testing.T) {
	// Must not panic and must not error on empty input.
	out, err := ToHTML("")
	if err != nil {
		t.Fatalf("ToHTML empty error: %v", err)
	}
	// Engine emits a single trailing newline for empty input.
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected empty/whitespace output for empty input, got %q", out)
	}
}

func TestToHTML_Concurrent(t *testing.T) {
	const goroutines = 16
	const iterations = 8
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*iterations)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				out, err := ToHTML("*bold* and /em/ and [l](https://x.test)")
				if err != nil {
					errs <- err
					return
				}
				if !strings.Contains(out, "<strong>bold</strong>") || !strings.Contains(out, "<em>em</em>") {
					errs <- &mismatchError{out}
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent ToHTML failure: %v", err)
	}
}

type mismatchError struct{ out string }

func (e *mismatchError) Error() string { return "unexpected output: " + e.out }

// nativeCarveBin locates the native carve-rs CLI for byte-identical checks.
// It is skipped (not failed) when the binary is unavailable, so the suite
// still runs in environments without a carve-rs checkout.
func nativeCarveBin(t *testing.T) string {
	t.Helper()
	if env := os.Getenv("CARVE_BIN"); env != "" {
		return env
	}
	candidates := []string{
		// Static-capable checkout (proto/div-label-fallback, the engine the
		// committed wasm is built from) is preferred so the static byte-check
		// can run; fall back to a plain main checkout for the interactive check.
		"/tmp/carve-rs-static/target/release/carve",
		"/media/mark/data/work/git/carve-rs/target/release/carve",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	if p, err := exec.LookPath("carve"); err == nil {
		return p
	}
	t.Skip("native carve binary not found; set CARVE_BIN to enable byte-identical check")
	return ""
}

func runNative(t *testing.T, bin, source string) string {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Stdin = strings.NewReader(source)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("native carve failed: %v", err)
	}
	return string(out)
}

func TestToHTML_ByteIdenticalToNative(t *testing.T) {
	bin := nativeCarveBin(t)
	samples := []string{
		"# Hi\n\nA paragraph with *bold*, /em/, and `code`.",
		"- one\n- two\n  - nested\n\n[link](https://example.com)",
		"| A | B |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |",
	}
	for i, s := range samples {
		want := runNative(t, bin, s)
		got, err := ToHTML(s)
		if err != nil {
			t.Fatalf("sample %d: ToHTML error: %v", i, err)
		}
		// The CLI appends a single trailing newline when output lacks one;
		// normalize a single trailing newline on both sides before compare.
		if normTrailingNL(got) != normTrailingNL(want) {
			t.Fatalf("sample %d byte mismatch:\n--- go ---\n%q\n--- native ---\n%q", i, got, want)
		}
	}
}

func normTrailingNL(s string) string {
	return strings.TrimRight(s, "\n") + "\n"
}

// --- static render mode ------------------------------------------------------

const detailsSrc = "::: details \"More info\"\nHidden body.\n:::\n"

// TestToHTMLStatic_DetailsOpen asserts the headline static behavior: a details
// block that is a collapsed <details> interactively becomes <details open> in
// static mode so the body is visible without a client.
func TestToHTMLStatic_DetailsOpen(t *testing.T) {
	// Interactive (with extensions so the <details> element is produced).
	interactive, err := ToHTMLOptions(detailsSrc, Options{Extensions: []string{"all"}})
	if err != nil {
		t.Fatalf("interactive error: %v", err)
	}
	if !strings.Contains(interactive, "<details>") {
		t.Fatalf("interactive: expected collapsed <details>, got %q", interactive)
	}
	if strings.Contains(interactive, "<details open>") {
		t.Fatalf("interactive: must NOT be open, got %q", interactive)
	}

	// Static: forced open.
	static, err := ToHTMLStatic(detailsSrc)
	if err != nil {
		t.Fatalf("static error: %v", err)
	}
	if !strings.Contains(static, "<details open>") {
		t.Fatalf("static: expected <details open>, got %q", static)
	}
	if !strings.Contains(static, "<summary>More info</summary>") {
		t.Fatalf("static: expected summary, got %q", static)
	}
}

// TestToHTMLStatic_SpoilerRevealed asserts an inline spoiler, which hides its
// content interactively, is revealed in static mode (the spoiler-revealed
// class is added). This stands in for the tabs/code-group "flatten" behavior:
// carve-rs has no Tabs/CodeGroup extension (those are carve-js / carve-php
// only), so spoiler reveal is the representative interactive-flatten case the
// embedded engine actually ships.
func TestToHTMLStatic_SpoilerRevealed(t *testing.T) {
	src := "Plot: :spoiler[the butler did it].\n"

	interactive, err := ToHTMLOptions(src, Options{Extensions: []string{"all"}})
	if err != nil {
		t.Fatalf("interactive error: %v", err)
	}
	if !strings.Contains(interactive, `<span class="spoiler">`) {
		t.Fatalf("interactive: expected hidden spoiler span, got %q", interactive)
	}

	static, err := ToHTMLStatic(src)
	if err != nil {
		t.Fatalf("static error: %v", err)
	}
	if !strings.Contains(static, `<span class="spoiler spoiler-revealed">`) {
		t.Fatalf("static: expected revealed spoiler span, got %q", static)
	}
}

// TestToHTMLStatic_MermaidSource asserts a mermaid fence degrades to its source
// as a <pre><code> block in static mode (no build-time image renderer is
// available across the WASI boundary).
func TestToHTMLStatic_MermaidSource(t *testing.T) {
	src := "``` mermaid\ngraph TD; A --> B\n```\n"

	static, err := ToHTMLStatic(src)
	if err != nil {
		t.Fatalf("static error: %v", err)
	}
	if !strings.Contains(static, "<pre") {
		t.Fatalf("static: expected <pre source fallback, got %q", static)
	}
	if !strings.Contains(static, `<code class="language-mermaid">`) {
		t.Fatalf("static: expected language-mermaid code, got %q", static)
	}
	// Source fallback must not emit any injected SVG/image (no renderer path).
	if strings.Contains(static, "<svg") || strings.Contains(static, "<img") {
		t.Fatalf("static: must degrade to source, not an image, got %q", static)
	}
}

// TestToHTMLStatic_DiffersFromInteractive sanity-checks that the two entry
// points actually diverge on the same input.
func TestToHTMLStatic_DiffersFromInteractive(t *testing.T) {
	interactive, err := ToHTMLOptions(detailsSrc, Options{Extensions: []string{"all"}})
	if err != nil {
		t.Fatalf("interactive error: %v", err)
	}
	static, err := ToHTMLStatic(detailsSrc)
	if err != nil {
		t.Fatalf("static error: %v", err)
	}
	if interactive == static {
		t.Fatalf("expected static and interactive output to differ, both = %q", static)
	}
}

// TestToHTMLOptions_StaticImpliesExtensions asserts that Options{Static: true}
// alone (no Extensions populated) still flattens an extension-backed construct,
// i.e. Static implies --extensions.
func TestToHTMLOptions_StaticImpliesExtensions(t *testing.T) {
	out, err := ToHTMLOptions(detailsSrc, Options{Static: true})
	if err != nil {
		t.Fatalf("ToHTMLOptions error: %v", err)
	}
	if !strings.Contains(out, "<details open>") {
		t.Fatalf("Static without Extensions must still flatten details, got %q", out)
	}
	// And it matches the convenience entry point.
	viaStatic, err := ToHTMLStatic(detailsSrc)
	if err != nil {
		t.Fatalf("ToHTMLStatic error: %v", err)
	}
	if out != viaStatic {
		t.Fatalf("Options{Static:true} must equal ToHTMLStatic: %q vs %q", out, viaStatic)
	}
}

// TestToHTML_DefaultUnchanged guards the non-breaking contract: the plain
// ToHTML path (interactive, no extensions) is unaffected by the new options.
func TestToHTML_DefaultUnchanged(t *testing.T) {
	out, err := ToHTML("# Hi")
	if err != nil {
		t.Fatalf("ToHTML error: %v", err)
	}
	viaOpts, err := ToHTMLOptions("# Hi", Options{})
	if err != nil {
		t.Fatalf("ToHTMLOptions error: %v", err)
	}
	if out != viaOpts {
		t.Fatalf("zero Options must equal ToHTML: %q vs %q", viaOpts, out)
	}
}

// TestToHTMLStatic_Concurrent asserts the static path is concurrency-safe.
func TestToHTMLStatic_Concurrent(t *testing.T) {
	const goroutines = 16
	const iterations = 8
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*iterations)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				out, err := ToHTMLStatic(detailsSrc)
				if err != nil {
					errs <- err
					return
				}
				if !strings.Contains(out, "<details open>") {
					errs <- &mismatchError{out}
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent ToHTMLStatic failure: %v", err)
	}
}

// runNativeStatic runs the native CLI with --html --static --extensions.
func runNativeStatic(t *testing.T, bin, source string) string {
	t.Helper()
	cmd := exec.Command(bin, "--html", "--static", "--extensions")
	cmd.Stdin = strings.NewReader(source)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("native carve --static failed: %v", err)
	}
	return string(out)
}

// TestToHTMLStatic_ByteIdenticalToNative asserts the Go static output equals
// the carve-rs CLI run with --html --static --extensions on the same input.
// It auto-skips unless a static-capable native binary is found (the binary
// must advertise --static).
func TestToHTMLStatic_ByteIdenticalToNative(t *testing.T) {
	bin := nativeCarveBin(t)
	if !binSupportsStatic(t, bin) {
		t.Skipf("native carve at %s does not support --static; set CARVE_BIN to a static-capable build", bin)
	}
	samples := []string{
		detailsSrc,
		"Plot: :spoiler[the butler did it].\n",
		"``` mermaid\ngraph TD; A --> B\n```\n",
		"::: spoiler \"Ending\"\nEveryone lives.\n:::\n",
	}
	for i, s := range samples {
		want := runNativeStatic(t, bin, s)
		got, err := ToHTMLStatic(s)
		if err != nil {
			t.Fatalf("sample %d: ToHTMLStatic error: %v", i, err)
		}
		if normTrailingNL(got) != normTrailingNL(want) {
			t.Fatalf("sample %d static byte mismatch:\n--- go ---\n%q\n--- native ---\n%q", i, got, want)
		}
	}
}

// binSupportsStatic reports whether the native binary advertises --static.
func binSupportsStatic(t *testing.T, bin string) bool {
	t.Helper()
	out, err := exec.Command(bin, "--help").CombinedOutput()
	if err != nil {
		// Some CLIs exit non-zero on --help; still inspect the output.
		return strings.Contains(string(out), "--static")
	}
	return strings.Contains(string(out), "--static")
}

// --- CodeCallouts (Tier-2 extension) -----------------------------------------

// TestToHTML_CodeCallouts asserts that <N> markers in a fenced code block are
// rendered as callout bubbles and the following paragraph of <N> descriptions
// becomes an ordered list, when the bundled extensions are enabled.
func TestToHTML_CodeCallouts(t *testing.T) {
	src := "``` go\nfmt.Println(\"hello\") // <1>\n```\n\n<1> prints a greeting\n"
	out, err := ToHTMLOptions(src, Options{Extensions: []string{"all"}})
	if err != nil {
		t.Fatalf("ToHTMLOptions error: %v", err)
	}
	if !strings.Contains(out, `<b class="callout"`) {
		t.Fatalf("expected callout bubble <b class=\"callout\">, got %q", out)
	}
	if !strings.Contains(out, `<ol class="callouts">`) {
		t.Fatalf("expected callout list <ol class=\"callouts\">, got %q", out)
	}
	if !strings.Contains(out, "prints a greeting") {
		t.Fatalf("expected callout text, got %q", out)
	}
}

// TestToHTML_CodeCallouts_NoExtensions asserts that without --extensions the
// code block is emitted verbatim (the <1> marker is not processed).
func TestToHTML_CodeCallouts_NoExtensions(t *testing.T) {
	src := "``` go\nfmt.Println(\"hello\") // <1>\n```\n\n<1> prints a greeting\n"
	out, err := ToHTML(src)
	if err != nil {
		t.Fatalf("ToHTML error: %v", err)
	}
	// No callout bubble; the raw marker text appears in the code block.
	if strings.Contains(out, `class="callout"`) {
		t.Fatalf("callout must not appear without --extensions, got %q", out)
	}
	if !strings.Contains(out, "<pre") {
		t.Fatalf("expected plain code block, got %q", out)
	}
}

// --- Citations (Tier-2 extension) ----------------------------------------
//
// Citations require the Rust-level Citations extension, which is a library API
// concern. The carve CLI (and thus the WASI shim) does not expose a
// --citations flag, so [@key] references and their in-document [@key]: defs
// render as ordinary mention spans and inline text via the WASI path. The
// tests below document this behavior and guard against regressions.

// TestToHTML_Citation_NotResolved confirms that a [@key] citation reference is
// rendered as a mention span by the WASI engine (Citations not in CLI bundle),
// and does NOT crash.
func TestToHTML_Citation_NotResolved(t *testing.T) {
	// A citation reference followed by an in-document definition.
	src := "See [@smith2020].\n\n[@smith2020]: Smith, J. (2020). Title.\n"
	out, err := ToHTMLOptions(src, Options{Extensions: []string{"all"}})
	if err != nil {
		t.Fatalf("ToHTMLOptions error: %v", err)
	}
	// Without the Citations extension the [@key] is parsed as a bracketed mention.
	if !strings.Contains(out, "smith2020") {
		t.Fatalf("expected smith2020 key in output, got %q", out)
	}
	// Must not crash or return an empty body.
	if strings.TrimSpace(out) == "" {
		t.Fatalf("expected non-empty output, got %q", out)
	}
}
