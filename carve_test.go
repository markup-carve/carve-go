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
